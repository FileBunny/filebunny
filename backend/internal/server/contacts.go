package server

import (
	"context"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
)

// A relay keeps every recipient as a contact (Mailjet does). For API relays
// with delete_contacts (the default) the server removes them once a day,
// after first putting addresses that unsubscribed or complained at the
// relay on its own block list: the relay replaces FileBunny's unsubscribe
// header with its own, so this is how those requests reach FileBunny.
const (
	contactsInterval = 24 * time.Hour
	contactsFirstRun = time.Minute
	// contactsMinAge leaves the relay a day for bounces, unsubscribes and
	// complaints before a contact goes.
	contactsMinAge = 24 * time.Hour
	// contactsPerRun caps the deletions per relay and day (one request
	// each); the rest follows the next day.
	contactsPerRun = 2000
	// contactsPause spaces the requests: Mailjet publishes no rate limits.
	contactsPause = 200 * time.Millisecond
	// contactsMaxFailures stops a run after this many failed deletions.
	contactsMaxFailures = 5
)

// contactsClient lists and deletes a relay's contacts; mail.Sender, or a
// test double.
type contactsClient interface {
	MailjetContacts(ctx context.Context, r mail.Relay, offset int) ([]mail.Contact, int, error)
	MailjetDeleteContact(ctx context.Context, r mail.Relay, id int64) error
}

// RunMailCleanup removes the relays' contacts once a day until ctx is
// canceled. It returns at once if no relay needs it.
func (s *Server) RunMailCleanup(ctx context.Context) {
	if s.mail == nil {
		return
	}
	var relays []mail.Relay
	for _, r := range s.mail.relays {
		if r.DeletesContacts() {
			relays = append(relays, r)
		}
	}
	if len(relays) == 0 {
		return
	}
	t := time.NewTimer(contactsFirstRun)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, r := range relays {
			s.cleanupContacts(ctx, r)
		}
		t.Reset(contactsInterval)
	}
}

// cleanupContacts deletes the contacts of r older than contactsMinAge.
// Addresses only ever reach the block list as an HMAC and are never logged.
func (s *Server) cleanupContacts(ctx context.Context, r mail.Relay) (deleted, blocked int) {
	m := s.mail
	cutoff := s.now().Add(-contactsMinAge)
	var failures int
	defer func() {
		if deleted > 0 || blocked > 0 || failures > 0 {
			s.log.Info("relay contacts removed", "relay", r.Name, "deleted", deleted, "blocked", blocked, "failed", failures)
		}
	}()
	pause := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(s.contactsPause):
			return true
		}
	}
	for offset := 0; ; offset += mail.ContactsPage {
		page, total, err := m.contacts.MailjetContacts(ctx, r, offset)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("listing relay contacts failed", "relay", r.Name, "err", err)
			}
			return
		}
		for _, c := range page {
			if c.Anonymized() || c.CreatedAt.IsZero() || c.CreatedAt.After(cutoff) {
				continue
			}
			if deleted >= contactsPerRun {
				return
			}
			if c.Unsubscribed || c.SpamComplaint {
				if mail.ValidAddress(c.Email) {
					if err := s.store.BlockMail(ctx, m.addressHash(c.Email), s.now()); err != nil {
						s.log.Error("blocking an address from the relay failed", "err", err)
						return // keep the contact, and with it the request, for the next run
					}
					blocked++
				}
			}
			err := m.contacts.MailjetDeleteContact(ctx, r, c.ID)
			switch {
			case err == nil:
				deleted++
			case mail.IsRateLimited(err):
				s.log.Warn("relay rate limit, contact cleanup continues tomorrow", "relay", r.Name)
				return
			default:
				failures++
				if failures >= contactsMaxFailures {
					s.log.Warn("contact cleanup stopped after failures", "relay", r.Name, "err", err)
					return
				}
			}
			if !pause() {
				return
			}
		}
		if len(page) == 0 || offset+len(page) >= total {
			return
		}
	}
}
