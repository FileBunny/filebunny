// Package logging builds loggers that never write client IP addresses.
//
// Handlers must not log client addresses in the first place. This package
// is the safety net for addresses that end up in error values anyway, such
// as "read tcp 10.0.0.2:8080->203.0.113.7:51234: i/o timeout".
package logging

import (
	"io"
	"log"
	"log/slog"
	"regexp"
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

// New returns a text logger that redacts IP addresses from the message and
// from every string or error attribute.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	}))
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return a // timestamps look like IPv6 to the pattern
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(RedactIPs(a.Value.String()))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(RedactIPs(err.Error()))
		} else {
			a.Value = slog.StringValue(RedactIPs(a.Value.String()))
		}
	}
	return a
}

// ErrorLog returns a logger for http.Server.ErrorLog. net/http writes client
// addresses into some messages (TLS handshake errors, recovered panics); l
// must be a logger from New so those are redacted.
func ErrorLog(l *slog.Logger) *log.Logger {
	return slog.NewLogLogger(l.With("source", "net/http").Handler(), slog.LevelWarn)
}
