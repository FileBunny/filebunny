package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
	"github.com/FileBunny/filebunny/backend/internal/ratelimit"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

// E-mail notifications: the sender of a share link can have the link
// mailed to an address, in a fixed text. The address is used for this one
// delivery and never stored or logged; limits and the block list use a
// keyed hash of it. Never log the address, its hash or the block token.
const (
	// maxMailSends caps deliveries in progress, server-wide.
	maxMailSends = 4
	// maxNotifyBody caps the request body.
	maxNotifyBody = 1 << 10
)

// mailSender delivers one message; mail.Sender, or a test double.
type mailSender interface {
	Send(ctx context.Context, r mail.Relay, from, to string, msg []byte) error
}

type mailer struct {
	from   string
	relays []mail.Relay
	sender mailSender
	pepper []byte
	// next is where the round-robin over the relays starts.
	next  atomic.Uint32
	slots chan struct{}
}

// EnableMail turns on e-mail notifications through the relays in c.
// pepper keys the address hashes (store.MailPepper).
func (s *Server) EnableMail(c mail.Config, pepper []byte) error {
	if len(c.Relays) == 0 {
		return nil
	}
	if s.cfg.PublicURL == "" {
		return errors.New("FILEBUNNY_PUBLIC_URL is required when mail is on")
	}
	s.mail = &mailer{from: c.From, relays: c.Relays, sender: mail.Sender{}, pepper: pepper,
		slots: make(chan struct{}, maxMailSends)}
	return nil
}

// addressHash is the keyed hash of an address; addresses differing only in
// case count as one.
func (m *mailer) addressHash(address string) []byte {
	h := hmac.New(sha256.New, m.pepper)
	h.Write([]byte(strings.ToLower(address)))
	return h.Sum(nil)
}

// blockToken is the address hash as it appears in the block link. Only a
// recipient of a mail (or the server) knows it.
func blockToken(hash []byte) string { return base64.RawURLEncoding.EncodeToString(hash) }

var blockTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type notifyRequest struct {
	Email string `json:"email"`
}

// handleNotify mails the link of a share link to {"email": "..."}. Only the
// sender may: it needs the delete token ("Authorization: Bearer").
func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	if s.mail == nil {
		writeError(w, http.StatusNotFound, "this server does not send e-mail")
		return
	}
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing delete token")
		return
	}
	var req notifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotifyBody)).Decode(&req); err != nil || !mail.ValidAddress(req.Email) {
		writeError(w, http.StatusBadRequest, `body must be {"email": "name@example.org"}`)
		return
	}
	now := s.now()
	f, err := s.store.ShareLink(r.Context(), r.PathValue("id"), token, now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "wrong delete token")
		return
	case err != nil:
		s.log.Error("notify failed", "err", err)
		writeError(w, http.StatusInternalServerError, "notify failed")
		return
	}

	m := s.mail
	hash := m.addressHash(req.Email)
	if blocked, err := s.store.MailBlocked(r.Context(), hash); err != nil {
		s.log.Error("notify failed", "err", err)
		writeError(w, http.StatusInternalServerError, "notify failed")
		return
	} else if blocked {
		writeError(w, http.StatusConflict, "this address asked not to get e-mail from FileBunny")
		return
	}
	if !s.proven(w, r, "mail") || !s.allow(w, r, ratelimit.Mail, 1) {
		return
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable, "too many e-mails on the way, try again in a minute")
		return
	}
	lim := store.MailLimits{PerFile: s.cfg.MailPerFile, PerRecipient: s.cfg.MailPerRecipient, Daily: s.cfg.MailDaily}
	switch err := s.store.ClaimMail(r.Context(), f.ID, hash, lim, now); {
	case errors.Is(err, store.ErrMailFileLimit):
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("this link was already mailed to %d addresses", s.cfg.MailPerFile))
		return
	case errors.Is(err, store.ErrMailRecipientLimit):
		writeError(w, http.StatusTooManyRequests, "this address got enough e-mail from FileBunny today")
		return
	case errors.Is(err, store.ErrMailDailyLimit):
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusServiceUnavailable, "FileBunny sent all e-mail it may send today, try again tomorrow")
		return
	case err != nil:
		s.log.Error("notify failed", "err", err)
		writeError(w, http.StatusInternalServerError, "notify failed")
		return
	}

	msg := s.notifyMessage(f, req.Email, hash, now)
	switch err := s.deliver(r.Context(), req.Email, msg, now); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errRecipientRefused):
		writeError(w, http.StatusUnprocessableEntity, "the recipient's mail server refused the address")
	default:
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusServiceUnavailable, "the e-mail could not be sent, try again later")
	}
}

var (
	errRecipientRefused = errors.New("recipient refused")
	errNoRelay          = errors.New("no relay could send")
)

// deliver tries the relays round-robin, skipping those that reached their
// daily limit, until one accepts the message.
func (s *Server) deliver(ctx context.Context, to string, msg []byte, now time.Time) error {
	m := s.mail
	start := int(m.next.Add(1) - 1)
	for i := range m.relays {
		relay := m.relays[(start+i)%len(m.relays)]
		ok, err := s.store.ClaimRelay(ctx, relay.Name, relay.DailyLimit, now)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		err = m.sender.Send(ctx, relay, m.from, to, msg)
		if err == nil {
			s.log.Debug("mail sent", "relay", relay.Name)
			return nil
		}
		// mail.SendError holds only the step and the SMTP code.
		s.log.Warn("mail relay failed", "relay", relay.Name, "err", err)
		if se, ok := errors.AsType[*mail.SendError](err); ok && se.Permanent() {
			return errRecipientRefused
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errNoRelay
}

// notifyMessage is the fixed mail. Nothing in it comes from the client:
// the link is built from the file ID, the address only goes in To.
func (s *Server) notifyMessage(f store.File, to string, hash []byte, now time.Time) []byte {
	base := s.cfg.PublicURL
	token := blockToken(hash)
	var b strings.Builder
	b.WriteString("Here is the link to the file sent to you:\n\n")
	b.WriteString(base + "/#/d/" + f.ID + "\n\n")
	b.WriteString("It can be downloaded until " + f.ExpiresAt.UTC().Format("2 January 2006, 15:04 MST"))
	switch left := f.DownloadsLeft(); {
	case left == 1:
		b.WriteString(", once")
	case left > 1:
		b.WriteString(fmt.Sprintf(", %d times", left))
	}
	b.WriteString(".\n\n")
	b.WriteString("The file is encrypted. To open it, use the password or the private key\n")
	b.WriteString("agreed with the sender. FileBunny never sends passwords by e-mail.\n\n")
	b.WriteString("-- \n")
	b.WriteString("FileBunny (" + base + ") encrypts files in the browser and shares\n")
	b.WriteString("them without accounts. This mail was sent because your address was\n")
	b.WriteString("entered when sharing the link. To get no more mail from FileBunny:\n")
	b.WriteString(base + "/#/block/" + token + "\n")

	id := make([]byte, 16)
	rand.Read(id)
	_, domain, _ := strings.Cut(s.mail.from, "@")
	return mail.Message{
		From:        s.mail.from,
		To:          to,
		Subject:     "Your file link from FileBunny",
		Body:        b.String(),
		Unsubscribe: base + "/api/v1/mail/block/" + token,
		Date:        now,
		MessageID:   hex.EncodeToString(id) + "@" + domain,
	}.Bytes()
}

// handleBlockMail puts the address behind the token on the block list.
// It also answers the one-click unsubscribe POST of mail clients (RFC 8058).
func (s *Server) handleBlockMail(w http.ResponseWriter, r *http.Request) {
	s.changeBlock(w, r, true)
}

// handleUnblockMail takes the address off the block list again.
func (s *Server) handleUnblockMail(w http.ResponseWriter, r *http.Request) {
	s.changeBlock(w, r, false)
}

func (s *Server) changeBlock(w http.ResponseWriter, r *http.Request, block bool) {
	// Read (and drop) a one-click body; it says nothing else.
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxNotifyBody))
	token := r.PathValue("token")
	if !blockTokenPattern.MatchString(token) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	hash, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if block {
		err = s.store.BlockMail(r.Context(), hash, s.now())
	} else {
		err = s.store.UnblockMail(r.Context(), hash)
	}
	if err != nil {
		s.log.Error("changing the mail block list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
