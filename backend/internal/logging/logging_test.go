package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
)

var leaks = []string{"203.0.113", "2001:db8", "198.51.100", "8a2e"}

func assertNoLeak(t *testing.T, s string) {
	t.Helper()
	for _, leak := range leaks {
		if strings.Contains(s, leak) {
			t.Errorf("%q contains %q", s, leak)
		}
	}
}

func TestRedactIPs(t *testing.T) {
	for _, in := range []string{
		"http: TLS handshake error from 203.0.113.7:51234: EOF",
		"http: panic serving [2001:db8::1]:443: boom",
		"connection from 2001:db8:85a3::8a2e:370:7334 closed",
		"peer ::ffff:198.51.100.2 reset",
	} {
		out := RedactIPs(in)
		assertNoLeak(t, out)
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%q -> %q: nothing redacted", in, out)
		}
	}
}

func TestLoggerRedactsErrorsAndMessages(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)

	opErr := &net.OpError{
		Op:     "read",
		Net:    "tcp",
		Source: &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 8080},
		Addr:   &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 51234},
		Err:    errors.New("i/o timeout"),
	}
	log.Error("upload failed", "err", opErr)
	log.Info("from 2001:db8::1", "peer", "198.51.100.2:443", "wrapped", errors.Join(errors.New("x"), opErr))

	assertNoLeak(t, buf.String())
	if !strings.Contains(buf.String(), "time=") || strings.Contains(buf.String(), "time=[redacted]") {
		t.Errorf("timestamp damaged: %s", buf.String())
	}
}

func TestErrorLogRedacts(t *testing.T) {
	var buf bytes.Buffer
	ErrorLog(New(&buf, slog.LevelInfo)).Printf("http: TLS handshake error from %s: EOF", "203.0.113.7:51234")
	assertNoLeak(t, buf.String())
	if !strings.Contains(buf.String(), "handshake") {
		t.Errorf("message lost: %s", buf.String())
	}
}
