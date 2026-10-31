package mail_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
	"github.com/FileBunny/filebunny/backend/internal/mail/mailtest"
)

func message(to string) []byte {
	return mail.Message{From: "noreply@example.org", To: to, Subject: "Test", Body: "line one\n.dot line\n",
		Unsubscribe: "https://example.org/api/v1/mail/block/abc", Date: time.Unix(0, 0), MessageID: "id@example.org"}.Bytes()
}

func TestSendStartTLSAndImplicit(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		srv := mailtest.Start(t, "fake", implicit)
		s := mail.Sender{TLS: srv.ClientTLS}
		if err := s.Send(t.Context(), srv.Relay, "noreply@example.org", "to@example.net", message("to@example.net")); err != nil {
			t.Fatalf("implicit=%v: %v", implicit, err)
		}
		got := srv.Messages()
		if len(got) != 1 || got[0].To != "to@example.net" || got[0].From != "noreply@example.org" {
			t.Fatalf("implicit=%v: got %+v", implicit, got)
		}
		for _, want := range []string{"Subject: Test\r\n", "\r\n.dot line\r\n", "List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"} {
			if !strings.Contains(got[0].Data, want) {
				t.Errorf("message lacks %q:\n%s", want, got[0].Data)
			}
		}
	}
}

// A relay whose certificate is not trusted is never used: the password
// would go to whoever is in between.
func TestSendRefusesUntrustedCertificate(t *testing.T) {
	srv := mailtest.Start(t, "fake", false)
	err := mail.Sender{}.Send(t.Context(), srv.Relay, "noreply@example.org", "to@example.net", message("to@example.net"))
	if err == nil || len(srv.Messages()) != 0 {
		t.Fatalf("sent over an untrusted connection: %v", err)
	}
}

// Errors carry the SMTP code only: relays echo the recipient in the text.
func TestSendErrorsHideReplyText(t *testing.T) {
	srv := mailtest.Start(t, "fake", false)
	srv.FailRcpt = 550
	err := mail.Sender{TLS: srv.ClientTLS}.Send(t.Context(), srv.Relay, "noreply@example.org", "secret@example.net", message("secret@example.net"))
	var se *mail.SendError
	if !errors.As(err, &se) || se.Code != 550 || !se.Permanent() {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error shows the address: %v", err)
	}

	srv.FailRcpt, srv.FailAuth = 0, true
	err = mail.Sender{TLS: srv.ClientTLS}.Send(t.Context(), srv.Relay, "noreply@example.org", "a@example.net", message("a@example.net"))
	if !errors.As(err, &se) || se.Step != "auth" || se.Permanent() {
		t.Fatalf("auth failure: %v", err)
	}
	if strings.Contains(err.Error(), "relay-secret") || strings.Contains(srv.Relay.String(), "relay-") {
		t.Error("error or relay name shows the account")
	}
}

func TestSendTimeout(t *testing.T) {
	r := mail.Relay{Name: "x", Host: "127.0.0.1", Port: 1, DailyLimit: 1}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (mail.Sender{Timeout: time.Second}).Send(ctx, r, "a@example.org", "b@example.org", nil); err == nil {
		t.Fatal("no error for a closed port")
	}
}

func TestValidAddress(t *testing.T) {
	good := []string{"a@example.org", "first.last+tag@sub.example.co.uk", "x_y-z@example.io"}
	bad := []string{
		"", "a", "@example.org", "a@", "a@example", "a@.example.org", "a@example..org", "a@b@example.org",
		"a@example.org\r\nBcc: x@example.org", "a b@example.org", "<a@example.org>", "A <a@example.org>",
		"a@[127.0.0.1]", `"a"@example.org`, "a@exämple.org", "a@example.org.", "a(comment)@example.org",
		strings.Repeat("a", 65) + "@example.org", "a@" + strings.Repeat("b", 250) + ".org",
	}
	for _, a := range good {
		if !mail.ValidAddress(a) {
			t.Errorf("refused %q", a)
		}
	}
	for _, a := range bad {
		if mail.ValidAddress(a) {
			t.Errorf("accepted %q", a)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	write := func(content string) string {
		p := filepath.Join(t.TempDir(), "smtp.json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c, err := mail.LoadConfig(write(`{"from": "noreply@example.org", "relays": [
		{"name": "one", "host": "smtp.example.org", "port": 587, "username": "u", "password": "p", "daily_limit": 300}]}`))
	if err != nil || len(c.Relays) != 1 || c.Relays[0].DailyLimit != 300 {
		t.Fatalf("got %+v, %v", c, err)
	}
	if c, err := mail.LoadConfig(write(`{"relays": []}`)); err != nil || len(c.Relays) != 0 {
		t.Errorf("no relays: %+v, %v", c, err)
	}
	for _, bad := range []string{
		`{"from": "noreply@example.org", "relays": [{"name": "One", "host": "h.example.org", "port": 587, "daily_limit": 1}]}`,
		`{"from": "noreply@example.org", "relays": [{"name": "a", "host": "h.example.org:25", "port": 587, "daily_limit": 1}]}`,
		`{"from": "noreply@example.org", "relays": [{"name": "a", "host": "h.example.org", "port": 0, "daily_limit": 1}]}`,
		`{"from": "noreply@example.org", "relays": [{"name": "a", "host": "h.example.org", "port": 587, "daily_limit": 0}]}`,
		`{"from": "noreply@example.org", "relays": [{"name": "a", "host": "h", "port": 1, "daily_limit": 1}, {"name": "a", "host": "h", "port": 1, "daily_limit": 1}]}`,
		`{"from": "FileBunny <noreply@example.org>", "relays": [{"name": "a", "host": "h", "port": 1, "daily_limit": 1}]}`,
		`{"from": "noreply@example.org", "relays": [], "extra": 1}`,
		`{"password": "do-not-show"`,
	} {
		_, err := mail.LoadConfig(write(bad))
		if err == nil {
			t.Errorf("accepted %s", bad)
		} else if strings.Contains(err.Error(), "do-not-show") {
			t.Errorf("error quotes the file: %v", err)
		}
	}
	if _, err := mail.LoadConfig(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file accepted")
	}
}
