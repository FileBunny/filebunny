package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the findings in docs/security-review.md.

func TestInjectionInIDIsHarmless(t *testing.T) {
	e := newTestEnv(t)
	up := e.upload(t, "keep me", "")

	for _, id := range []string{
		"' OR '1'='1",
		"x' UNION SELECT delete_token_hash FROM files--",
		"AAAAAAAAAAAAAAAAAAAA'--",
		"%2e%2e%2ffilebunny.db",
		"$(id)",
		"<script>alert(1)</script>",
	} {
		path := "/api/v1/files/" + url.PathEscape(id)
		if rec := e.do("GET", path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %q: status %d", id, rec.Code)
		} else if strings.Contains(rec.Body.String(), "<script>") {
			t.Errorf("GET %q: input reflected in body: %s", id, rec.Body)
		}
		if rec := e.do("DELETE", path, nil, "Authorization", "Bearer x' OR '1'='1"); rec.Code != http.StatusNotFound {
			t.Errorf("DELETE %q: status %d", id, rec.Code)
		}
	}

	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Body.String() != "keep me" {
		t.Errorf("existing file damaged: %d %q", rec.Code, rec.Body)
	}
}

// A delete token only works for its own file (no IDOR between uploads).
func TestDeleteTokenIsBoundToFile(t *testing.T) {
	e := newTestEnv(t)
	a := e.upload(t, "a", "")
	b := e.upload(t, "b", "")

	rec := e.do("DELETE", "/api/v1/files/"+b.ID, nil, "Authorization", "Bearer "+a.DeleteToken)
	if rec.Code != http.StatusForbidden {
		t.Errorf("token of A on B: status %d", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/files/"+b.ID, nil); rec.Code != http.StatusOK {
		t.Errorf("B gone: status %d", rec.Code)
	}
}

func TestUploadResponseIsNotCacheable(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do("POST", "/api/v1/files", strings.NewReader("x"))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
}

func TestWrongMethod(t *testing.T) {
	e := newTestEnv(t)
	for _, m := range []string{"PUT", "PATCH"} {
		if rec := e.do(m, "/api/v1/files", strings.NewReader("x")); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", m, rec.Code)
		}
	}
}

func TestStorageQuota(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.MaxTotalSize = 2 * testMaxSize
	full := strings.Repeat("a", testMaxSize)

	first := e.upload(t, full, "")
	e.upload(t, full, "")
	if rec := e.do("POST", "/api/v1/files", strings.NewReader("x")); rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("over quota: status %d", rec.Code)
	}

	e.do("DELETE", "/api/v1/files/"+first.ID, nil, "Authorization", "Bearer "+first.DeleteToken)
	e.upload(t, full, "")
}

// Uploads in progress count against the quota, so parallel uploads cannot
// together overshoot it.
func TestStorageQuotaReservations(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.MaxTotalSize = 3 * testMaxSize

	var mu sync.Mutex
	var granted int
	var releases []func()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			release, err := e.srv.reserve(t.Context(), testMaxSize)
			if err == nil {
				mu.Lock()
				granted++
				releases = append(releases, release)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != 3 {
		t.Errorf("granted %d reservations, want 3", granted)
	}
	for _, r := range releases {
		r()
	}
	if e.srv.reserved != 0 {
		t.Errorf("reserved %d after release", e.srv.reserved)
	}
}

// A client that sends headers and then stalls is cut off, and nothing is
// left behind on disk.
func TestStalledUploadIsAborted(t *testing.T) {
	e := newTestEnv(t)
	e.srv.stall = 200 * time.Millisecond
	ts := httptest.NewServer(e.srv)
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("POST /api/v1/files HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\npartial"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no response within 5s: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("status %d, want 400: %v %s", resp.StatusCode, resp.Header, b)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if strings.Contains(body["error"], "127.0.0.1") {
		t.Errorf("error leaks address: %q", body["error"])
	}

	for _, sub := range []string{"tmp", "blobs"} {
		entries, _ := os.ReadDir(filepath.Join(e.dir, sub))
		if len(entries) != 0 {
			t.Errorf("%s not empty: %v", sub, entries)
		}
	}
	if e.srv.reserved != 0 {
		t.Errorf("reservation not released: %d", e.srv.reserved)
	}
}
