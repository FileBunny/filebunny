package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	if strings.Contains(e.logs.String(), addr) {
		t.Errorf("client address in logs:\n%s", e.logs)
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
