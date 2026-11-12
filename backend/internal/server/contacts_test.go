package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
)

// fakeContacts is a relay account in memory.
type fakeContacts struct {
	contacts []mail.Contact
	deleted  []int64
	pages    int
	// failFrom makes deletions fail from this many on; rateLimit answers 429.
	failFrom  int
	rateLimit bool
}

func (f *fakeContacts) MailjetContacts(_ context.Context, _ mail.Relay, offset int) ([]mail.Contact, int, error) {
	f.pages++
	end := min(offset+mail.ContactsPage, len(f.contacts))
	if offset >= len(f.contacts) {
		return nil, len(f.contacts), nil
	}
	return f.contacts[offset:end], len(f.contacts), nil
}

func (f *fakeContacts) MailjetDeleteContact(_ context.Context, _ mail.Relay, id int64) error {
	if f.rateLimit {
		return &mail.SendError{Via: mail.TypeMailjetAPI, Step: "delete contact", Code: 429}
	}
	if f.failFrom > 0 && len(f.deleted) >= f.failFrom {
		return &mail.SendError{Via: mail.TypeMailjetAPI, Step: "delete contact", Code: 500}
	}
	f.deleted = append(f.deleted, id)
	return nil
}

var apiTestRelay = mail.Relay{Name: "mj", Type: mail.TypeMailjetAPI, Username: "k", Password: "s", DailyLimit: 10}

func withContacts(t *testing.T, e *testEnv, f *fakeContacts) {
	t.Helper()
	e.srv.cfg.PublicURL = "https://filebunny.example"
	if err := e.srv.EnableMail(mail.Config{From: "noreply@filebunny.example", Relays: []mail.Relay{apiTestRelay}}, []byte("test pepper")); err != nil {
		t.Fatal(err)
	}
	e.srv.mail.contacts = f
	e.srv.contactsPause = 0
}

func TestCleanupContacts(t *testing.T) {
	e := newTestEnv(t)
	now := e.clock
	old, recent := now.Add(-25*time.Hour), now.Add(-time.Hour)
	f := &fakeContacts{contacts: []mail.Contact{
		{ID: 1, Email: "old@example.org", CreatedAt: old},
		{ID: 2, Email: "new@example.org", CreatedAt: recent},                    // too recent
		{ID: 3, Email: "x1@domain.invalid", CreatedAt: old},                     // already anonymized
		{ID: 4, Email: "unsub@example.org", CreatedAt: old, Unsubscribed: true}, // to the block list
		{ID: 5, Email: "spam@example.org", CreatedAt: old, SpamComplaint: true}, // to the block list
		{ID: 6, Email: "nodate@example.org"},                                    // no creation time: kept
	}}
	withContacts(t, e, f)

	deleted, blocked := e.srv.cleanupContacts(t.Context(), apiTestRelay)
	if deleted != 3 || blocked != 2 || fmt.Sprint(f.deleted) != "[1 4 5]" {
		t.Fatalf("deleted %d %v, blocked %d", deleted, f.deleted, blocked)
	}
	for addr, want := range map[string]bool{"unsub@example.org": true, "spam@example.org": true, "old@example.org": false, "UNSUB@example.org": true} {
		got, err := e.srv.store.MailBlocked(t.Context(), e.srv.mail.addressHash(addr))
		if err != nil || got != want {
			t.Errorf("%s blocked=%v, want %v (%v)", addr, got, want, err)
		}
	}
	if logs := e.logs.String(); strings.Contains(logs, "@example.org") || !strings.Contains(logs, "relay contacts removed") {
		t.Errorf("logs: %s", logs)
	}
}

func TestCleanupContactsPagesAndCap(t *testing.T) {
	e := newTestEnv(t)
	f := &fakeContacts{}
	for i := range contactsPerRun + 500 {
		f.contacts = append(f.contacts, mail.Contact{ID: int64(i + 1), Email: fmt.Sprintf("a%d@example.org", i), CreatedAt: e.clock.Add(-48 * time.Hour)})
	}
	withContacts(t, e, f)
	deleted, _ := e.srv.cleanupContacts(t.Context(), apiTestRelay)
	if deleted != contactsPerRun || len(f.deleted) != contactsPerRun || f.pages < 2 {
		t.Errorf("deleted %d in %d pages, cap %d", deleted, f.pages, contactsPerRun)
	}
}

func TestCleanupContactsStops(t *testing.T) {
	old := mail.Contact{Email: "a@example.org"}
	t.Run("rate limit", func(t *testing.T) {
		e := newTestEnv(t)
		old.CreatedAt = e.clock.Add(-48 * time.Hour)
		f := &fakeContacts{contacts: []mail.Contact{old, old, old}, rateLimit: true}
		withContacts(t, e, f)
		if deleted, _ := e.srv.cleanupContacts(t.Context(), apiTestRelay); deleted != 0 {
			t.Errorf("deleted %d", deleted)
		}
		if !strings.Contains(e.logs.String(), "rate limit") {
			t.Errorf("logs: %s", e.logs.String())
		}
	})
	t.Run("failures", func(t *testing.T) {
		e := newTestEnv(t)
		old.CreatedAt = e.clock.Add(-48 * time.Hour)
		f := &fakeContacts{failFrom: 1}
		for range 20 {
			f.contacts = append(f.contacts, old)
		}
		withContacts(t, e, f)
		if deleted, _ := e.srv.cleanupContacts(t.Context(), apiTestRelay); deleted != 1 {
			t.Errorf("deleted %d, want 1 before the failures", deleted)
		}
		if !strings.Contains(e.logs.String(), "stopped after failures") {
			t.Errorf("logs: %s", e.logs.String())
		}
	})
}

func TestCleanupContactsOnlyForAPIRelays(t *testing.T) {
	off := false
	for _, tc := range []struct {
		r    mail.Relay
		want bool
	}{
		{apiTestRelay, true},
		{mail.Relay{Name: "x", Type: mail.TypeMailjetAPI, DeleteContacts: &off}, false},
		{mail.Relay{Name: "smtp", Host: "smtp.example.org", Port: 587}, false},
	} {
		if got := tc.r.DeletesContacts(); got != tc.want {
			t.Errorf("%s: %v", tc.r.Name, got)
		}
	}
}
