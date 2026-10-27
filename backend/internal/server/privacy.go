package server

import (
	"log"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// ipPattern matches IPv4 and IPv6 addresses, with or without port. It errs
// on the side of redacting too much.
var ipPattern = regexp.MustCompile(
	`\[[0-9A-Fa-f:.%a-z]+\](:\d+)?` + // [2001:db8::1]:443
		`|\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b` + // 192.0.2.1:443
		`|[0-9A-Fa-f]{0,4}(:[0-9A-Fa-f]{0,4}){2,7}`) // 2001:db8::1

// RedactIPs replaces anything that looks like an IP address.
func RedactIPs(s string) string {
	return ipPattern.ReplaceAllString(s, "[redacted]")
}

// ErrorLog returns a logger for http.Server.ErrorLog. net/http writes client
// addresses into some messages (TLS handshake errors, recovered panics);
// those are redacted before they reach the log.
func ErrorLog(l *slog.Logger) *log.Logger {
	return log.New(redactWriter{l}, "", 0)
}

type redactWriter struct{ l *slog.Logger }

func (w redactWriter) Write(p []byte) (int, error) {
	w.l.Warn("http server", "detail", RedactIPs(strings.TrimSpace(string(p))))
	return len(p), nil
}

// securityHeaders sets headers for API responses. Headers for the frontend
// (CSP, HSTS) are set by Caddy.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
