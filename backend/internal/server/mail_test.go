package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
	"github.com/FileBunny/filebunny/backend/internal/mail/mailtest"
)

// withMail turns mail on with fake relays; the first answers.
func withMail(t *testing.T, e *testEnv, relays ...*mailtest.Server) {
	t.Helper()
	e.srv.cfg.PublicURL = "https://filebunny.example"
	c := mail.Config{From: "noreply@filebunny.example"}
	for _, r := range relays {
		c.Relays = append(c.Relays, r.Relay)
	}
	if err := e.srv.EnableMail(c, []byte("test pepper")); err != nil {
		t.Fatal(err)
	}
	// All fakes share no certificate; trust each one's own.
	e.srv.mail.sender = fakeTrust(relays)
}

type fakeTrust []*mailtest.Server

func (f fakeTrust) Deliver(ctx context.Context, r mail.Relay, m mail.Message) error {
	for _, s := range f {
		if s.Relay.Name == r.Name {
			return mail.Sender{TLS: s.ClientTLS}.Deliver(ctx, r, m)
		}
	}
	panic("unknown relay " + r.Name)
}

func (e *testEnv) notify(up uploadResponse, email string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"email": email})
	return e.do("POST", "/api/v1/files/"+up.ID+"/notify", strings.NewReader(string(body)), "Authorization", "Bearer "+up.DeleteToken)
}

func TestNotify(t *testing.T) {
	e := newTestEnv(t)
	up := e.upload(t, "x", "?max_downloads=5&expires=48h")
	if rec := e.notify(up, "to@example.net"); rec.Code != http.StatusNotFound {
		t.Errorf("mail off: %d", rec.Code)
	}
	relay := mailtest.Start(t, "one", false)
	withMail(t, e, relay)
	if !strings.Contains(e.do("GET", "/api/v1/features", nil).Body.String(), `"mail":true`) {
		t.Error("features do not say mail is on")
	}

	if rec := e.notify(up, "to@example.net"); rec.Code != http.StatusNoContent {
		t.Fatalf("notify: %d %s", rec.Code, rec.Body)
	}
	got := relay.Messages()
	if len(got) != 1 || got[0].To != "to@example.net" || got[0].From != "noreply@filebunny.example" {
		t.Fatalf("delivered %+v", got)
	}
	data := got[0].Data
	for _, want := range []string{
		"Subject: Your file link from FileBunny\r\n",
		"Here is the link to the file sent to you:\r\n",
		"https://filebunny.example/#/d/" + up.ID + "\r\n",
		"until 29 October 2026, 12:00 UTC, 5 times.",
		"https://filebunny.example/#/block/",
		"List-Unsubscribe: <https://filebunny.example/api/v1/mail/block/",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("mail lacks %q:\n%s", want, data)
		}
	}
	// Neither the delete token nor a download: the link only.
	if strings.Contains(data, up.DeleteToken) {
		t.Error("mail contains the delete token")
	}
	if rec := e.do("HEAD", "/api/v1/files/"+up.ID, nil); rec.Header().Get("Downloads-Remaining") != "5" {
		t.Error("notify counted a download")
	}
}

func TestNotifyRefusals(t *testing.T) {
	e := newTestEnv(t)
	relay := mailtest.Start(t, "one", false)
	withMail(t, e, relay)
	up := e.upload(t, "x", "")

	for _, bad := range []string{"", "nope", "a@example.org\r\nBcc: b@example.org", "A <a@example.org>"} {
		if rec := e.notify(up, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", bad, rec.Code)
		}
	}
	wrong := up
	wrong.DeleteToken = "wrong"
	if rec := e.notify(wrong, "a@example.org"); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", rec.Code)
	}
	if rec := e.do("POST", "/api/v1/files/"+up.ID+"/notify", strings.NewReader(`{"email":"a@example.org"}`)); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	missing := up
	missing.ID = "AAAAAAAAAAAAAAAAAAAAAA"
	if rec := e.notify(missing, "a@example.org"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown link: %d", rec.Code)
	}
	if len(relay.Messages()) != 0 {
		t.Fatal("a refused request sent mail")
	}

	// Per link: three addresses, then 429.
	for _, to := range []string{"a@example.org", "b@example.org", "c@example.org"} {
		if rec := e.notify(up, to); rec.Code != http.StatusNoContent {
			t.Fatalf("%s: %d %s", to, rec.Code, rec.Body)
		}
	}
	if rec := e.notify(up, "d@example.org"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("fourth address: %d", rec.Code)
	}
	// Per recipient: three a day, whatever the case.
	for range 2 {
		e.notify(e.upload(t, "x", ""), "A@Example.org")
	}
	if rec := e.notify(e.upload(t, "x", ""), "a@example.org"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("fourth mail to one address: %d", rec.Code)
	}
	// Server-wide per day.
	e.srv.cfg.MailDaily = 5 // five sent so far
	if rec := e.notify(e.upload(t, "x", ""), "z@example.org"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("over the daily limit: %d", rec.Code)
	}
	e.clock = e.clock.Add(24 * time.Hour)
	if rec := e.notify(e.upload(t, "x", ""), "z@example.org"); rec.Code != http.StatusNoContent {
		t.Errorf("next day: %d %s", rec.Code, rec.Body)
	}
}

// Round-robin over the relays; a full or failing relay is skipped, a
// refused recipient is not retried elsewhere.
func TestNotifyRelays(t *testing.T) {
	e := newTestEnv(t)
	e.srv.cfg.MailPerFile = 100
	one, two := mailtest.Start(t, "one", false), mailtest.Start(t, "two", true)
	one.Relay.DailyLimit = 2
	withMail(t, e, one, two)
	up := e.upload(t, "x", "")
	send := func(to string) int { return e.notify(up, to).Code }

	for _, to := range []string{"a@example.org", "b@example.org", "c@example.org", "d@example.org"} {
		if code := send(to); code != http.StatusNoContent {
			t.Fatalf("%s: %d", to, code)
		}
	}
	if len(one.Messages()) != 2 || len(two.Messages()) != 2 {
		t.Fatalf("one %d, two %d; want 2 and 2", len(one.Messages()), len(two.Messages()))
	}
	// one is full now: everything goes through two.
	if code := send("e@example.org"); code != http.StatusNoContent || len(two.Messages()) != 3 {
		t.Errorf("one full: %d, two has %d", code, len(two.Messages()))
	}

	two.FailMail = 451
	if code := send("f@example.org"); code != http.StatusServiceUnavailable {
		t.Errorf("all relays out: %d", code)
	}
	two.FailMail, two.FailRcpt = 0, 550
	if code := send("g@example.org"); code != http.StatusUnprocessableEntity {
		t.Errorf("recipient refused: %d", code)
	}
	if strings.Contains(e.logs.String(), "g@example.org") || strings.Contains(e.logs.String(), "relay-secret") {
		t.Errorf("address or password in logs:\n%s", e.logs)
	}
}

func TestMailBlock(t *testing.T) {
	e := newTestEnv(t)
	relay := mailtest.Start(t, "one", false)
	withMail(t, e, relay)
	if rec := e.notify(e.upload(t, "x", ""), "to@example.net"); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body)
	}
	data := relay.Messages()[0].Data
	_, rest, _ := strings.Cut(data, "/api/v1/mail/block/")
	token := rest[:43]
	if !strings.Contains(data, "/#/block/"+token) {
		t.Fatal("block link and one-click link differ")
	}

	// One-click, as a mail client does it.
	rec := e.do("POST", "/api/v1/mail/block/"+token, strings.NewReader("List-Unsubscribe=One-Click"),
		"Content-Type", "application/x-www-form-urlencoded")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("block: %d", rec.Code)
	}
	if rec := e.notify(e.upload(t, "x", ""), "TO@example.net"); rec.Code != http.StatusConflict {
		t.Errorf("blocked address: %d", rec.Code)
	}
	if rec := e.do("DELETE", "/api/v1/mail/block/"+token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unblock: %d", rec.Code)
	}
	if rec := e.notify(e.upload(t, "x", ""), "to@example.net"); rec.Code != http.StatusNoContent {
		t.Errorf("after unblock: %d", rec.Code)
	}
	for _, bad := range []string{"short", strings.Repeat("!", 43)} {
		if rec := e.do("POST", "/api/v1/mail/block/"+bad, nil); rec.Code != http.StatusNotFound {
			t.Errorf("token %q: %d", bad, rec.Code)
		}
	}
}

func TestEnableMailNeedsPublicURL(t *testing.T) {
	e := newTestEnv(t)
	c := mail.Config{From: "noreply@filebunny.example", Relays: []mail.Relay{{Name: "x", Host: "h", Port: 1, DailyLimit: 1}}}
	if err := e.srv.EnableMail(c, []byte("p")); err == nil {
		t.Error("mail on without FILEBUNNY_PUBLIC_URL")
	}
	if err := e.srv.EnableMail(mail.Config{}, nil); err != nil || e.srv.mail != nil {
		t.Errorf("no relays: %v", err)
	}
}
