package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

const testMaxSize = 1024

type testEnv struct {
	srv   *Server
	clock time.Time
	logs  *bytes.Buffer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg, _ := config.FromEnv(func(string) string { return "" })
	cfg.MaxSize = testMaxSize

	env := &testEnv{clock: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC), logs: &bytes.Buffer{}}
	env.srv = New(cfg, st, slog.New(slog.NewTextHandler(env.logs, nil)))
	env.srv.now = func() time.Time { return env.clock }
	return env
}

func (e *testEnv) do(method, target string, body io.Reader, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) upload(t *testing.T, body string, query string) uploadResponse {
	t.Helper()
	rec := e.do("POST", "/api/v1/files"+query, strings.NewReader(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: status %d: %s", rec.Code, rec.Body)
	}
	var resp uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	if rec := e.do("GET", "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("status %d", rec.Code)
	}
}

func TestUploadDownload(t *testing.T) {
	e := newTestEnv(t)
	up := e.upload(t, "-----BEGIN PGP MESSAGE-----", "")

	if want := e.clock.Add(24 * time.Hour); !up.ExpiresAt.Equal(want) {
		t.Errorf("expires_at %v, want default %v", up.ExpiresAt, want)
	}

	rec := e.do("GET", "/api/v1/files/"+up.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status %d", rec.Code)
	}
	if rec.Body.String() != "-----BEGIN PGP MESSAGE-----" {
		t.Errorf("body %q", rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content-type %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control %q", got)
	}
}

func TestUploadExpires(t *testing.T) {
	e := newTestEnv(t)
	up := e.upload(t, "x", "?expires=1h")
	if want := e.clock.Add(time.Hour); !up.ExpiresAt.Equal(want) {
		t.Errorf("expires_at %v, want %v", up.ExpiresAt, want)
	}

	e.clock = e.clock.Add(time.Hour)
	if rec := e.do("GET", "/api/v1/files/"+up.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("after expiry: status %d", rec.Code)
	}
}

func TestUploadBadExpires(t *testing.T) {
	e := newTestEnv(t)
	for _, q := range []string{"7d", "169h", "0s", "-1h", "abc"} {
		rec := e.do("POST", "/api/v1/files?expires="+q, strings.NewReader("x"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expires=%s: status %d", q, rec.Code)
		}
	}
}

func TestUploadSizeLimit(t *testing.T) {
	e := newTestEnv(t)
	atLimit := strings.Repeat("a", testMaxSize)
	overLimit := atLimit + "a"

	if rec := e.do("POST", "/api/v1/files", strings.NewReader(atLimit)); rec.Code != http.StatusCreated {
		t.Errorf("at limit: status %d", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/files", strings.NewReader(overLimit)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over limit (Content-Length): status %d", rec.Code)
	}

	// Without Content-Length (chunked), the limit is enforced while streaming.
	req := httptest.NewRequest("POST", "/api/v1/files", io.NopCloser(strings.NewReader(overLimit)))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over limit (streaming): status %d", rec.Code)
	}
}

func TestDownloadUnknown(t *testing.T) {
	e := newTestEnv(t)
	for _, id := range []string{"AAAAAAAAAAAAAAAAAAAAAA", "..%2Ffilebunny.db", "x"} {
		if rec := e.do("GET", "/api/v1/files/"+id, nil); rec.Code != http.StatusNotFound {
			t.Errorf("id %q: status %d", id, rec.Code)
		}
	}
}

func TestDelete(t *testing.T) {
	e := newTestEnv(t)
	up := e.upload(t, "x", "")
	path := "/api/v1/files/" + up.ID

	if rec := e.do("DELETE", path, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d", rec.Code)
	}
	if rec := e.do("DELETE", path, nil, "Authorization", "Bearer wrong"); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token: status %d", rec.Code)
	}
	if rec := e.do("DELETE", path, nil, "Authorization", "Bearer "+up.DeleteToken); rec.Code != http.StatusNoContent {
		t.Errorf("delete: status %d", rec.Code)
	}
	if rec := e.do("GET", path, nil); rec.Code != http.StatusNotFound {
		t.Errorf("after delete: status %d", rec.Code)
	}
}
