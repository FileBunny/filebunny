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
)

func TestRedactIPs(t *testing.T) {
	for _, in := range []string{
		"http: TLS handshake error from 203.0.113.7:51234: EOF",
		"http: panic serving [2001:db8::1]:443: boom",
		"connection from 2001:db8:85a3::8a2e:370:7334 closed",
		"peer ::ffff:198.51.100.2 reset",
	} {
		out := RedactIPs(in)
		for _, leak := range []string{"203.0.113", "2001:db8", "198.51.100", "8a2e"} {
			if strings.Contains(out, leak) {
				t.Errorf("%q -> %q still contains %q", in, out, leak)
			}
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%q -> %q: nothing redacted", in, out)
		}
	}
}

func TestErrorLogRedacts(t *testing.T) {
	var buf bytes.Buffer
	ErrorLog(slog.New(slog.NewTextHandler(&buf, nil))).Printf("http: TLS handshake error from %s: EOF", "203.0.113.7:51234")
	if strings.Contains(buf.String(), "203.0.113.7") {
		t.Errorf("address leaked: %s", buf.String())
	}
}

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
	send("GET", "/healthz", nil)

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
			!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Errorf("%s: missing security headers: %v", target, h)
		}
	}
	if rec := e.do("GET", "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown route: status %d", rec.Code)
	}
}
