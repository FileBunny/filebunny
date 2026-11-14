package mail_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FileBunny/filebunny/backend/internal/mail"
)

func apiMessage(to string) mail.Message {
	return mail.Message{From: "noreply@example.org", To: to, Subject: "Test", Body: "line one\n.dot line\n",
		Unsubscribe: "https://example.org/api/v1/mail/block/abc"}
}

var apiRelay = mail.Relay{Name: "mj", Type: mail.TypeMailjetAPI, Username: "key", Password: "secret", DailyLimit: 10}

// apiSender points a Sender at a fake Mailjet API.
func apiSender(t *testing.T, h http.HandlerFunc) mail.Sender {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return mail.Sender{APIBase: srv.URL, TLS: srv.Client().Transport.(*http.Transport).TLSClientConfig}
}

func TestMailjetAPIRequest(t *testing.T) {
	var got struct {
		Messages []struct {
			From                    struct{ Email, Name string }
			To                      []struct{ Email string }
			Subject, TextPart       string
			Headers                 map[string]string
			TrackOpens, TrackClicks string
			HTMLPart                *string
		}
	}
	s := apiSender(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if r.Method != http.MethodPost || r.URL.Path != "/v3.1/send" || !ok || user != "key" || pass != "secret" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request: %s %s auth=%v", r.Method, r.URL.Path, ok)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		io.WriteString(w, `{"Messages":[{"Status":"success","To":[{"Email":"to@example.net","MessageID":1}]}]}`)
	})
	if err := s.Deliver(t.Context(), apiRelay, apiMessage("to@example.net")); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages: %+v", got)
	}
	m := got.Messages[0]
	switch {
	case m.From.Email != "noreply@example.org" || m.From.Name != "FileBunny":
		t.Errorf("from: %+v", m.From)
	case len(m.To) != 1 || m.To[0].Email != "to@example.net":
		t.Errorf("to: %+v", m.To)
	case m.Subject != "Test" || m.TextPart != "line one\n.dot line\n" || m.HTMLPart != nil:
		t.Errorf("content: %q %q html=%v", m.Subject, m.TextPart, m.HTMLPart)
	case m.TrackOpens != "disabled" || m.TrackClicks != "disabled":
		t.Errorf("tracking: %q %q", m.TrackOpens, m.TrackClicks)
	case m.Headers["List-Unsubscribe"] != "<https://example.org/api/v1/mail/block/abc>" ||
		m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" || m.Headers["Auto-Submitted"] != "auto-generated":
		t.Errorf("headers: %v", m.Headers)
	}
}

func TestMailjetAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   int
	}{
		{"auth", http.StatusUnauthorized, `{"ErrorMessage":"secret@example.net is not allowed"}`, 401},
		{"bad request", http.StatusBadRequest, `{"Messages":[{"Status":"error","Errors":[{"ErrorMessage":"secret@example.net"}]}]}`, 400},
		{"not success", http.StatusOK, `{"Messages":[{"Status":"error"}]}`, 0},
		{"garbage", http.StatusOK, `secret@example.net`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := apiSender(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			err := s.Deliver(t.Context(), apiRelay, apiMessage("secret@example.net"))
			var se *mail.SendError
			if !errors.As(err, &se) || se.Via != mail.TypeMailjetAPI || se.Code != tc.code {
				t.Fatalf("got %v (%+v)", err, se)
			}
			if se.Permanent() {
				t.Error("an API error must not count as permanent")
			}
			if strings.Contains(err.Error(), "secret@") {
				t.Errorf("the error quotes the response: %v", err)
			}
		})
	}
}

func TestMailjetAPINoRedirect(t *testing.T) {
	followed := false
	var s mail.Sender
	s = apiSender(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			io.WriteString(w, `{"Messages":[{"Status":"success"}]}`)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	if err := s.Deliver(t.Context(), apiRelay, apiMessage("to@example.net")); err == nil {
		t.Error("a redirect was accepted")
	}
	if followed {
		t.Error("the redirect was followed")
	}
}

func TestLoadConfigRelayTypes(t *testing.T) {
	for _, tc := range []struct {
		name, relay string
		ok          bool
	}{
		{"smtp default", `{"name":"a","host":"smtp.example.org","port":587,"daily_limit":1}`, true},
		{"smtp explicit", `{"name":"a","type":"smtp","host":"smtp.example.org","port":587,"daily_limit":1}`, true},
		{"api", `{"name":"a","type":"mailjet-api","username":"k","password":"s","daily_limit":1}`, true},
		{"api with host", `{"name":"a","type":"mailjet-api","host":"evil.example","username":"k","password":"s","daily_limit":1}`, false},
		{"api with port", `{"name":"a","type":"mailjet-api","port":443,"username":"k","password":"s","daily_limit":1}`, false},
		{"api without secret", `{"name":"a","type":"mailjet-api","username":"k","daily_limit":1}`, false},
		{"unknown type", `{"name":"a","type":"carrier-pigeon","host":"x.example","port":1,"daily_limit":1}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "smtp.json")
			os.WriteFile(path, []byte(`{"from":"noreply@example.org","relays":[`+tc.relay+`]}`), 0o600)
			_, err := mail.LoadConfig(path)
			if (err == nil) != tc.ok {
				t.Errorf("ok=%v, err=%v", tc.ok, err)
			}
		})
	}
}

func TestMailjetContacts(t *testing.T) {
	var deleted []string
	s := apiSender(t, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "key" || p != "secret" {
			t.Errorf("auth on %s", r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v3/REST/contact":
			if r.URL.Query().Get("Limit") != "1000" || r.URL.Query().Get("Offset") != "1000" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			// IDs come as numbers or strings.
			io.WriteString(w, `{"Count":2,"Total":1002,"Data":[
				{"ID":7,"Email":"a@example.org","CreatedAt":"2026-11-02T21:08:00Z","UnsubscribedAt":"","IsSpamComplaining":false},
				{"ID":"8","Email":"b@example.org","CreatedAt":"2026-11-02T21:15:00Z","UnsubscribedAt":"2026-11-03T10:00:00Z","IsSpamComplaining":true}]}`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v4/contacts/"):
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/v4/contacts/"))
			if strings.HasSuffix(r.URL.Path, "/9") {
				w.WriteHeader(http.StatusTooManyRequests)
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	page, total, err := s.MailjetContacts(t.Context(), apiRelay, 1000)
	if err != nil || total != 1002 || len(page) != 2 {
		t.Fatalf("page %+v total %d err %v", page, total, err)
	}
	if a, b := page[0], page[1]; a.ID != 7 || a.Unsubscribed || a.SpamComplaint || a.CreatedAt.IsZero() ||
		b.ID != 8 || !b.Unsubscribed || !b.SpamComplaint {
		t.Errorf("contacts %+v", page)
	}
	if err := s.MailjetDeleteContact(t.Context(), apiRelay, 7); err != nil {
		t.Error(err)
	}
	if err := s.MailjetDeleteContact(t.Context(), apiRelay, 9); !mail.IsRateLimited(err) {
		t.Errorf("429 not seen as a rate limit: %v", err)
	}
	if strings.Join(deleted, ",") != "7,9" {
		t.Errorf("deleted %v", deleted)
	}
	if !(mail.Contact{Email: "x1@domain.invalid"}).Anonymized() || (mail.Contact{Email: "a@example.org"}).Anonymized() {
		t.Error("Anonymized")
	}
}

func TestDeleteContactsOnlyForAPIRelays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smtp.json")
	os.WriteFile(path, []byte(`{"from":"noreply@example.org","relays":[{"name":"a","host":"smtp.example.org","port":587,"daily_limit":1,"delete_contacts":true}]}`), 0o600)
	if _, err := mail.LoadConfig(path); err == nil {
		t.Error("delete_contacts accepted for an SMTP relay")
	}
	os.WriteFile(path, []byte(`{"from":"noreply@example.org","relays":[{"name":"a","type":"mailjet-api","username":"k","password":"s","daily_limit":1,"delete_contacts":false}]}`), 0o600)
	c, err := mail.LoadConfig(path)
	if err != nil || c.Relays[0].DeletesContacts() {
		t.Errorf("delete_contacts false: %v %v", err, c.Relays)
	}
}
