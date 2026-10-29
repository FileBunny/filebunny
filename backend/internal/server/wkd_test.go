package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/FileBunny/filebunny/backend/internal/wkd"
)

// fakeWKD answers lookups without network access.
type fakeWKD struct {
	key   []byte
	err   error
	block chan struct{} // if set, lookups wait for it to close
	start chan struct{} // if set, receives one value per lookup started
	got   []wkd.Address
	mu    sync.Mutex
}

func (f *fakeWKD) Lookup(ctx context.Context, a wkd.Address) ([]byte, error) {
	f.mu.Lock()
	f.got = append(f.got, a)
	f.mu.Unlock()
	if f.start != nil {
		f.start <- struct{}{}
	}
	if f.block != nil {
		<-f.block
	}
	return f.key, f.err
}

func wkdBody(email string) *strings.Reader {
	b, _ := json.Marshal(map[string]string{"email": email})
	return strings.NewReader(string(b))
}

func TestWKD(t *testing.T) {
	e := newTestEnv(t)
	f := &fakeWKD{key: []byte("key bytes")}
	e.srv.wkd = f

	rec := e.do("POST", "/api/v1/wkd", wkdBody("Alice@Example.ORG"))
	if rec.Code != http.StatusOK || rec.Body.String() != "key bytes" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", rec.Header())
	}
	if len(f.got) != 1 || f.got[0] != (wkd.Address{Local: "Alice", Domain: "example.org"}) {
		t.Errorf("looked up %+v", f.got)
	}
}

func TestWKDErrors(t *testing.T) {
	cases := map[string]struct {
		body   string
		err    error
		status int
	}{
		"not found":       {`{"email":"a@example.org"}`, wkd.ErrNotFound, http.StatusNotFound},
		"upstream":        {`{"email":"a@example.org"}`, wkd.ErrUpstream, http.StatusBadGateway},
		"invalid address": {`{"email":"a@127.0.0.1"}`, nil, http.StatusBadRequest},
		"with port":       {`{"email":"a@example.org:8443"}`, nil, http.StatusBadRequest},
		"not json":        {`email=a@example.org`, nil, http.StatusBadRequest},
		"too large":       {`{"email":"` + strings.Repeat("a", 2000) + `@example.org"}`, nil, http.StatusBadRequest},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			f := &fakeWKD{err: c.err}
			e.srv.wkd = f
			rec := e.do("POST", "/api/v1/wkd", strings.NewReader(c.body))
			if rec.Code != c.status {
				t.Errorf("status %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Errorf("no JSON error: %s", rec.Body)
			}
			if strings.Contains(rec.Body.String(), "example") {
				t.Errorf("input echoed: %s", rec.Body)
			}
			if c.status == http.StatusBadRequest && len(f.got) != 0 {
				t.Error("invalid request looked up")
			}
		})
	}
}

func TestWKDSwitchedOff(t *testing.T) {
	e := newTestEnv(t)
	e.srv.wkd = nil
	if rec := e.do("POST", "/api/v1/wkd", wkdBody("a@example.org")); rec.Code != http.StatusNotFound {
		t.Errorf("status %d", rec.Code)
	}
	rec := e.do("GET", "/api/v1/features", nil)
	if strings.TrimSpace(rec.Body.String()) != `{"wkd":false}` {
		t.Errorf("features %s", rec.Body)
	}
	e.srv.wkd = &fakeWKD{}
	rec = e.do("GET", "/api/v1/features", nil)
	if strings.TrimSpace(rec.Body.String()) != `{"wkd":true}` {
		t.Errorf("features %s", rec.Body)
	}
}

func TestWKDMethod(t *testing.T) {
	e := newTestEnv(t)
	e.srv.wkd = &fakeWKD{}
	if rec := e.do("GET", "/api/v1/wkd?email=a@example.org", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", rec.Code)
	}
}

// At most maxWKDLookups run at once; the next gets 503 straight away.
func TestWKDConcurrencyCap(t *testing.T) {
	e := newTestEnv(t)
	f := &fakeWKD{key: []byte("k"), block: make(chan struct{}), start: make(chan struct{}, maxWKDLookups)}
	e.srv.wkd = f

	var wg sync.WaitGroup
	codes := make(chan int, maxWKDLookups)
	for range maxWKDLookups {
		wg.Go(func() { codes <- e.do("POST", "/api/v1/wkd", wkdBody("a@example.org")).Code })
	}
	for range maxWKDLookups {
		<-f.start
	}
	if rec := e.do("POST", "/api/v1/wkd", wkdBody("a@example.org")); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("over the cap: status %d", rec.Code)
	}
	close(f.block)
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Errorf("status %d", c)
		}
	}
	f.block, f.start = nil, nil
	if rec := e.do("POST", "/api/v1/wkd", wkdBody("a@example.org")); rec.Code != http.StatusOK {
		t.Errorf("slot not released: status %d", rec.Code)
	}
}

func TestWKDCORSForNullOrigin(t *testing.T) {
	e := newTestEnv(t)
	e.srv.wkd = &fakeWKD{key: []byte("k")}
	rec := e.do("OPTIONS", "/api/v1/wkd", nil, "Origin", "null", "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "content-type")
	if rec.Header().Get("Access-Control-Allow-Origin") != "null" || !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight %d %v", rec.Code, rec.Header())
	}
	rec = e.do("POST", "/api/v1/wkd", wkdBody("a@example.org"), "Origin", "https://evil.example")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS for another origin")
	}
}
