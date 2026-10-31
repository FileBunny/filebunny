package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FileBunny/filebunny/backend/internal/wkd"
)

// TestNoClientAddressInLogs runs every endpoint, including error paths, and
// checks that the client address never reaches the log.
func TestNoClientAddressInLogs(t *testing.T) {
	e := newTestEnv(t)
	const addr = "203.0.113.7"

	send := func(method, target string, body io.Reader, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, body)
		req.RemoteAddr = addr + ":51234"
		req.Header.Set("X-Forwarded-For", addr)
		req.Header.Set("X-Real-IP", addr)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		e.srv.ServeHTTP(rec, req)
		return rec
	}

	rec := send("POST", "/api/v1/files", strings.NewReader("x"))
	var up uploadResponse
	json.Unmarshal(rec.Body.Bytes(), &up)
	send("GET", "/api/v1/files/"+up.ID, nil)
	send("GET", "/api/v1/files/unknown", nil)
	send("POST", "/api/v1/files?expires=bad", strings.NewReader("x"))
	send("POST", "/api/v1/files", strings.NewReader(strings.Repeat("a", testMaxSize+1)))
	send("DELETE", "/api/v1/files/"+up.ID, nil, "Authorization", "Bearer wrong")
	send("DELETE", "/api/v1/files/"+up.ID, nil, "Authorization", "Bearer "+up.DeleteToken)
	send("HEAD", "/api/v1/files/unknown", nil)
	send("GET", "/healthz", nil)

	rec = send("POST", "/api/v1/uploads?max_downloads=1", nil)
	var cu createUploadResponse
	json.Unmarshal(rec.Body.Bytes(), &cu)
	auth := "Bearer " + cu.UploadToken
	send("PUT", "/api/v1/uploads/"+cu.ID+"?offset=0", strings.NewReader("x"), "Authorization", auth)
	send("PUT", "/api/v1/uploads/"+cu.ID+"?offset=9", strings.NewReader("x"), "Authorization", auth)
	send("PUT", "/api/v1/uploads/"+cu.ID+"?offset=1", strings.NewReader("x"), "Authorization", "Bearer wrong")
	send("POST", "/api/v1/uploads/"+cu.ID+"/complete", nil, "Authorization", auth)
	send("DELETE", "/api/v1/uploads/unknown", nil, "Authorization", auth)
	send("POST", "/api/v1/uploads?expires=bad", nil)

	// WKD: neither the client nor the looked-up address, its domain or
	// hash may be logged, on any outcome.
	const email = "looked.up@private-domain.example"
	for _, err := range []error{nil, wkd.ErrNotFound, wkd.ErrUpstream} {
		e.srv.wkd = &fakeWKD{key: []byte("k"), err: err}
		send("POST", "/api/v1/wkd", wkdBody(email))
	}
	send("POST", "/api/v1/wkd", wkdBody("looked.up@private-domain.example:1"))
	send("GET", "/api/v1/features", nil)

	// Upload links: no link ID, management token or file ID either.
	rec = send("POST", "/api/v1/inboxes", strings.NewReader(testPublicKey))
	var in createInboxResponse
	json.Unmarshal(rec.Body.Bytes(), &in)
	manage := "Bearer " + in.ManageToken
	send("GET", "/api/v1/inboxes/"+in.ID, nil)
	send("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader("x"))
	send("POST", "/api/v1/inboxes/"+in.ID+"/files", strings.NewReader(""))
	rec = send("GET", "/api/v1/inboxes/"+in.ID+"/files", nil, "Authorization", manage)
	var list manageInboxResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Files) != 1 {
		t.Fatalf("upload link list: %s", rec.Body)
	}
	inboxFile := list.Files[0].ID
	send("GET", "/api/v1/inboxes/"+in.ID+"/files/"+inboxFile, nil, "Authorization", manage)
	send("GET", "/api/v1/inboxes/"+in.ID+"/files", nil, "Authorization", "Bearer wrong")
	send("DELETE", "/api/v1/inboxes/"+in.ID+"/files/"+inboxFile, nil, "Authorization", manage)
	send("POST", "/api/v1/uploads?inbox="+in.ID, nil)
	send("DELETE", "/api/v1/inboxes/"+in.ID, nil, "Authorization", manage)
	send("POST", "/api/v1/inboxes", strings.NewReader("not a key"))

	looked, _ := wkd.ParseAddress(email)
	for _, secret := range []string{addr, "looked.up", "private-domain", looked.Hash(), in.ID, in.ManageToken, inboxFile} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("%q in logs:\n%s", secret, e.logs)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newTestEnv(t)
	for _, target := range []string{"/healthz", "/api/v1/files/unknown", "/nope"} {
		rec := e.do("GET", target, nil)
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "no-referrer" ||
			!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") ||
			!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: missing security headers: %v", target, h)
		}
	}
	if rec := e.do("GET", "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown route: status %d", rec.Code)
	}
}

func TestCORSOnlyForNullOrigin(t *testing.T) {
	e := newTestEnv(t)

	// Preflight from a page opened from disk.
	rec := e.do("OPTIONS", "/api/v1/uploads", nil,
		"Origin", "null", "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "authorization")
	h := rec.Header()
	if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "null" ||
		!strings.Contains(h.Get("Access-Control-Allow-Methods"), "PUT") ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight: %d %v", rec.Code, h)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must never be allowed")
	}

	rec = e.do("POST", "/api/v1/files", strings.NewReader("x"), "Origin", "null")
	if rec.Code != http.StatusCreated || rec.Header().Get("Access-Control-Allow-Origin") != "null" ||
		!strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "Downloads-Remaining") {
		t.Errorf("upload from null origin: %d %v", rec.Code, rec.Header())
	}

	// Any real origin, or none: no CORS headers, and no preflight answer.
	for _, origin := range []string{"https://evil.example", "https://filebunny.eu", ""} {
		rec := e.do("OPTIONS", "/api/v1/uploads", nil, "Origin", origin, "Access-Control-Request-Method", "POST")
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Code == http.StatusNoContent {
			t.Errorf("origin %q: preflight %d %v", origin, rec.Code, rec.Header())
		}
		rec = e.do("POST", "/api/v1/files", strings.NewReader("x"), "Origin", origin)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q got CORS headers", origin)
		}
	}
}
