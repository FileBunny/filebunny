package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func (e *testEnv) createUpload(t *testing.T, query string) createUploadResponse {
	t.Helper()
	rec := e.do("POST", "/api/v1/uploads"+query, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create upload: status %d: %s", rec.Code, rec.Body)
	}
	var resp createUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func (e *testEnv) chunk(up createUploadResponse, offset int, body string) *httptest.ResponseRecorder {
	return e.do("PUT", "/api/v1/uploads/"+up.ID+"?offset="+strconv.Itoa(offset), strings.NewReader(body),
		"Authorization", "Bearer "+up.UploadToken)
}

func (e *testEnv) complete(up createUploadResponse) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/uploads/"+up.ID+"/complete", nil, "Authorization", "Bearer "+up.UploadToken)
}

// pending returns the upload with this ID, or nil.
func (e *testEnv) pending(id string) *pendingUpload {
	e.srv.upMu.Lock()
	defer e.srv.upMu.Unlock()
	return e.srv.uploads[id]
}

func (e *testEnv) assertNoPending(t *testing.T) {
	t.Helper()
	e.srv.upMu.Lock()
	n := len(e.srv.uploads)
	e.srv.upMu.Unlock()
	if n != 0 {
		t.Errorf("%d uploads still pending", n)
	}
	e.srv.mu.Lock()
	reserved := e.srv.reserved
	e.srv.mu.Unlock()
	if reserved != 0 {
		t.Errorf("reserved %d, want 0", reserved)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.dir, "tmp")); len(entries) != 0 {
		t.Errorf("tmp not empty: %v", entries)
	}
}

func TestChunkedUpload(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "?expires=1h&max_downloads=1")
	if up.MaxChunkSize != maxChunkSize || up.MaxSize != testMaxSize || len(up.ID) != 22 || len(up.UploadToken) != 43 {
		t.Errorf("create response %+v", up)
	}

	for i, part := range []string{"-----BEGIN ", "PGP MESSAGE-----"} {
		offset := len("-----BEGIN ") * i
		rec := e.chunk(up, offset, part)
		var resp chunkResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if rec.Code != http.StatusOK || resp.Offset != int64(offset+len(part)) {
			t.Fatalf("chunk %d: %d %s", i, rec.Code, rec.Body)
		}
	}

	rec := e.complete(up)
	if rec.Code != http.StatusCreated {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	var file uploadResponse
	json.Unmarshal(rec.Body.Bytes(), &file)
	if file.Size != 27 || file.MaxDownloads != 1 || !file.ExpiresAt.Equal(e.clock.Add(time.Hour)) || file.DeleteToken == "" {
		t.Errorf("complete response %+v", file)
	}
	e.assertNoPending(t)

	if rec := e.do("GET", "/api/v1/files/"+file.ID, nil); rec.Body.String() != "-----BEGIN PGP MESSAGE-----" {
		t.Errorf("download: %d %q", rec.Code, rec.Body)
	}
	if rec := e.complete(up); rec.Code != http.StatusNotFound {
		t.Errorf("complete twice: status %d", rec.Code)
	}
}

func TestChunkedUploadBadLimits(t *testing.T) {
	e := newTestEnv(t)
	for _, q := range []string{"?expires=8d", "?max_downloads=0"} {
		if rec := e.do("POST", "/api/v1/uploads"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, rec.Code)
		}
	}
	e.assertNoPending(t)
}

func TestChunkWrongOffset(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")
	e.chunk(up, 0, "abc")
	for _, offset := range []int{0, 2, 4} {
		rec := e.chunk(up, offset, "x")
		var resp chunkResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if rec.Code != http.StatusConflict || resp.Offset != 3 {
			t.Errorf("offset %d: %d %s", offset, rec.Code, rec.Body)
		}
	}
	for _, q := range []string{"", "?offset=-1", "?offset=abc"} {
		rec := e.do("PUT", "/api/v1/uploads/"+up.ID+q, strings.NewReader("x"), "Authorization", "Bearer "+up.UploadToken)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d", q, rec.Code)
		}
	}
}

// A chunk that fails halfway leaves nothing behind, so the client can send
// the same chunk again at the same offset.
func TestChunkRetryAfterFailure(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")
	e.chunk(up, 0, "first-")

	req := httptest.NewRequest("PUT", "/api/v1/uploads/"+up.ID+"?offset=6",
		io.MultiReader(strings.NewReader("sec"), failingReader{}))
	req.ContentLength = 6
	req.Header.Set("Authorization", "Bearer "+up.UploadToken)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("failed chunk: status %d %s", rec.Code, rec.Body)
	}
	if e.srv.reserved != 6 {
		t.Errorf("reserved %d after failed chunk, want 6", e.srv.reserved)
	}

	if rec := e.chunk(up, 6, "second"); rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	var file uploadResponse
	json.Unmarshal(e.complete(up).Body.Bytes(), &file)
	if rec := e.do("GET", "/api/v1/files/"+file.ID, nil); rec.Body.String() != "first-second" {
		t.Errorf("body %q", rec.Body)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestChunkLimits(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")

	// The whole upload may not exceed the max size.
	if rec := e.chunk(up, 0, strings.Repeat("a", testMaxSize)); rec.Code != http.StatusOK {
		t.Fatalf("chunk at max size: %d", rec.Code)
	}
	if rec := e.chunk(up, testMaxSize, "a"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("past max size: status %d", rec.Code)
	}

	// A single chunk may not exceed maxChunkSize, checked before reading.
	e.srv.cfg.MaxSize = 2 * maxChunkSize
	req := httptest.NewRequest("PUT", "/api/v1/uploads/"+up.ID+"?offset="+strconv.Itoa(testMaxSize), strings.NewReader("x"))
	req.ContentLength = maxChunkSize + 1
	req.Header.Set("Authorization", "Bearer "+up.UploadToken)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunk too large: status %d", rec.Code)
	}

	// Without Content-Length the quota cannot be reserved up front.
	req = httptest.NewRequest("PUT", "/api/v1/uploads/"+up.ID+"?offset="+strconv.Itoa(testMaxSize), io.NopCloser(strings.NewReader("x")))
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+up.UploadToken)
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusLengthRequired {
		t.Errorf("no Content-Length: status %d", rec.Code)
	}
}

func TestEmptyUploadCannotComplete(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")
	if rec := e.complete(up); rec.Code != http.StatusBadRequest {
		t.Errorf("status %d", rec.Code)
	}
	// Still open: a chunk can follow.
	if rec := e.chunk(up, 0, "x"); rec.Code != http.StatusOK {
		t.Errorf("chunk after empty complete: status %d", rec.Code)
	}
}

func TestUploadTokenIsBoundToUpload(t *testing.T) {
	e := newTestEnv(t)
	a := e.createUpload(t, "")
	b := e.createUpload(t, "")
	path := "/api/v1/uploads/" + b.ID + "?offset=0"

	if rec := e.do("PUT", path, strings.NewReader("x")); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d", rec.Code)
	}
	if rec := e.do("PUT", path, strings.NewReader("x"), "Authorization", "Bearer "+a.UploadToken); rec.Code != http.StatusForbidden {
		t.Errorf("token of A on B: status %d", rec.Code)
	}
	for _, id := range []string{"AAAAAAAAAAAAAAAAAAAAAA", "..%2F..%2Ffilebunny.db", "x"} {
		rec := e.do("PUT", "/api/v1/uploads/"+id+"?offset=0", strings.NewReader("x"), "Authorization", "Bearer "+a.UploadToken)
		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q: status %d", id, rec.Code)
		}
	}
	if rec := e.do("DELETE", "/api/v1/uploads/"+b.ID, nil, "Authorization", "Bearer "+a.UploadToken); rec.Code != http.StatusForbidden {
		t.Errorf("abort B with token of A: status %d", rec.Code)
	}
}

func TestConcurrentRequestOnSameUpload(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")
	u := e.pending(up.ID)
	u.mu.Lock() // a chunk in progress
	rec := e.chunk(up, 0, "x")
	u.mu.Unlock()
	if rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", rec.Code)
	}
	if rec := e.chunk(up, 0, "x"); rec.Code != http.StatusOK {
		t.Errorf("after the other request: status %d", rec.Code)
	}
}

func TestAbortUpload(t *testing.T) {
	e := newTestEnv(t)
	up := e.createUpload(t, "")
	e.chunk(up, 0, "data")
	rec := e.do("DELETE", "/api/v1/uploads/"+up.ID, nil, "Authorization", "Bearer "+up.UploadToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("abort: status %d", rec.Code)
	}
	e.assertNoPending(t)
	if rec := e.chunk(up, 4, "more"); rec.Code != http.StatusNotFound {
		t.Errorf("chunk after abort: status %d", rec.Code)
	}
}

func TestMaxPendingUploads(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.MaxPendingUploads = 2
	first := e.createUpload(t, "")
	e.createUpload(t, "")

	rec := e.do("POST", "/api/v1/uploads", nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("over the cap: %d %v", rec.Code, rec.Header())
	}

	e.chunk(first, 0, "x")
	e.complete(first)
	e.createUpload(t, "") // the slot is free again
}

// Chunks hold quota until the upload completes or is aborted, so pending
// uploads cannot together overshoot it.
func TestChunkedUploadQuota(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.MaxTotalSize = 2 * testMaxSize
	full := strings.Repeat("a", testMaxSize)

	a := e.createUpload(t, "")
	b := e.createUpload(t, "")
	e.chunk(a, 0, full)
	e.chunk(b, 0, full)
	if rec := e.do("POST", "/api/v1/files", strings.NewReader("x")); rec.Code != http.StatusInsufficientStorage {
		t.Errorf("single upload over quota: status %d", rec.Code)
	}
	c := e.createUpload(t, "")
	if rec := e.chunk(c, 0, "x"); rec.Code != http.StatusInsufficientStorage {
		t.Errorf("chunk over quota: status %d", rec.Code)
	}

	// Completed: the bytes now count as stored, not reserved.
	e.complete(a)
	if e.srv.reserved != testMaxSize {
		t.Errorf("reserved %d after complete, want %d", e.srv.reserved, testMaxSize)
	}
	e.do("DELETE", "/api/v1/uploads/"+b.ID, nil, "Authorization", "Bearer "+b.UploadToken)
	if rec := e.chunk(c, 0, "x"); rec.Code != http.StatusOK {
		t.Errorf("chunk after abort freed quota: status %d", rec.Code)
	}
}

// An upload that receives nothing for uploadIdleTimeout gives back its slot,
// quota and temp file; activity keeps it open.
func TestIdleUploadExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newTestEnv(t)
		idle := e.createUpload(t, "")
		active := e.createUpload(t, "")
		e.chunk(idle, 0, "abc")

		for i := range 3 {
			time.Sleep(uploadIdleTimeout / 2)
			if rec := e.chunk(active, i, "x"); rec.Code != http.StatusOK {
				t.Fatalf("active chunk: %d", rec.Code)
			}
		}
		synctest.Wait()

		if rec := e.chunk(idle, 3, "d"); rec.Code != http.StatusNotFound {
			t.Errorf("idle upload still open: status %d", rec.Code)
		}
		if rec := e.complete(active); rec.Code != http.StatusCreated {
			t.Errorf("active upload: status %d %s", rec.Code, rec.Body)
		}
		e.assertNoPending(t)
	})
}
