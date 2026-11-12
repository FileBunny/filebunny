// Package mail sends FileBunny's fixed notification mails through relays
// the operator configures: SMTP relays, or a relay's HTTPS API (api.go)
// where the server's provider blocks outgoing SMTP.
//
// Only TLS connections are used: for SMTP implicit TLS on port 465,
// STARTTLS (which must be offered) on any other port. Relay hosts come from
// the operator's configuration file, API addresses are fixed; neither ever
// comes from a client. Errors never carry the relay's reply text, which may
// echo the recipient address; see SendError.
package mail

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"time"
)

// Relay is one relay from the configuration file.
type Relay struct {
	// Name identifies the relay in logs and counters; never the account.
	Name string `json:"name"`
	// Type is TypeSMTP (or empty) or TypeMailjetAPI.
	Type     string `json:"type"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// DailyLimit is the most mails sent through this relay per UTC day;
	// set it to the relay's own (free tier) limit.
	DailyLimit int `json:"daily_limit"`
	// ImplicitTLS connects with TLS right away, as on port 465 (always
	// used there); otherwise STARTTLS is required.
	ImplicitTLS bool `json:"implicit_tls"`
	// DeleteContacts (API relays, default true) removes the contacts the
	// relay keeps of every recipient, once a day; see server.cleanupContacts.
	DeleteContacts *bool `json:"delete_contacts"`
}

// DeletesContacts reports whether the contact cleanup runs for r.
func (r Relay) DeletesContacts() bool {
	return r.kind() == TypeMailjetAPI && (r.DeleteContacts == nil || *r.DeleteContacts)
}

func (r Relay) implicitTLS() bool { return r.ImplicitTLS || r.Port == 465 }

func (r Relay) kind() string {
	if r.Type == "" {
		return TypeSMTP
	}
	return r.Type
}

// String never shows the account.
func (r Relay) String() string { return r.Name }

// Config is the content of the file FILEBUNNY_SMTP_FILE names.
type Config struct {
	// From is the sender address, e.g. noreply@filebunny.eu.
	From   string  `json:"from"`
	Relays []Relay `json:"relays"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// maxConfigSize caps the configuration file.
const maxConfigSize = 64 << 10

// LoadConfig reads and checks the configuration file. A file without relays
// is valid and means mail is off.
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("mail config: %w", err)
	}
	defer f.Close()
	var c Config
	dec := json.NewDecoder(io.LimitReader(f, maxConfigSize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		// The decoder's message may quote the file; keep secrets out.
		return Config{}, errors.New("mail config: not valid JSON in the expected shape")
	}
	if len(c.Relays) == 0 {
		return c, nil
	}
	if !ValidAddress(c.From) {
		return Config{}, errors.New("mail config: from must be a plain address such as noreply@example.org")
	}
	seen := map[string]bool{}
	for i, r := range c.Relays {
		switch {
		case !namePattern.MatchString(r.Name):
			return Config{}, fmt.Errorf("mail config: relay %d: name must be 1-32 of a-z, 0-9 and -", i+1)
		case seen[r.Name]:
			return Config{}, fmt.Errorf("mail config: relay %s: name used twice", r.Name)
		case r.DailyLimit < 1:
			return Config{}, fmt.Errorf("mail config: relay %s: daily_limit must be at least 1", r.Name)
		}
		switch r.kind() {
		case TypeSMTP:
			switch {
			case r.Host == "" || strings.ContainsAny(r.Host, "/: \r\n"):
				return Config{}, fmt.Errorf("mail config: relay %s: host must be a host name", r.Name)
			case r.Port < 1 || r.Port > 65535:
				return Config{}, fmt.Errorf("mail config: relay %s: port out of range", r.Name)
			case r.DeleteContacts != nil:
				return Config{}, fmt.Errorf("mail config: relay %s: delete_contacts is for API relays only", r.Name)
			}
		case TypeMailjetAPI:
			// The API address is fixed: a host or port here would be ignored.
			switch {
			case r.Host != "" || r.Port != 0 || r.ImplicitTLS:
				return Config{}, fmt.Errorf("mail config: relay %s: an API relay takes no host, port or implicit_tls", r.Name)
			case r.Username == "" || r.Password == "":
				return Config{}, fmt.Errorf("mail config: relay %s: an API relay needs username and password (the API key and secret)", r.Name)
			}
		default:
			return Config{}, fmt.Errorf("mail config: relay %s: type must be %q or %q", r.Name, TypeSMTP, TypeMailjetAPI)
		}
		seen[r.Name] = true
	}
	return c, nil
}

// maxAddressLength is the SMTP limit for a path (RFC 5321).
const maxAddressLength = 254

// ValidAddress accepts plain ASCII addresses such as name@example.org: no
// display name, comments, quotes, IP literals or line breaks, so an address
// can only ever be a recipient and never change a header or command.
func ValidAddress(s string) bool {
	if len(s) == 0 || len(s) > maxAddressLength {
		return false
	}
	for _, c := range s {
		if c <= ' ' || c >= 0x7f || strings.ContainsRune(`"()<>[],;:\`, c) {
			return false
		}
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || strings.Contains(domain, "@") || len(local) > 64 {
		return false
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s
}

// Message is a plain-text mail. All fields come from the server.
type Message struct {
	From, To, Subject string
	// Body is ASCII text with \n line breaks.
	Body string
	// Unsubscribe is the one-click blocking URL (RFC 8058).
	Unsubscribe string
	Date        time.Time
	// MessageID without the angle brackets.
	MessageID string
}

// Bytes renders the message with CRLF line endings.
func (m Message) Bytes() []byte {
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", "FileBunny <"+m.From+">")
	h("To", m.To)
	h("Subject", m.Subject)
	h("Date", m.Date.UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+m.MessageID+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=us-ascii")
	h("Content-Transfer-Encoding", "7bit")
	h("Auto-Submitted", "auto-generated")
	if m.Unsubscribe != "" {
		h("List-Unsubscribe", "<"+m.Unsubscribe+">")
		h("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	b.WriteString("\r\n")
	// The SMTP data writer escapes lines starting with a dot.
	for line := range strings.Lines(m.Body) {
		b.WriteString(strings.TrimSuffix(line, "\n") + "\r\n")
	}
	return []byte(b.String())
}

// SendError is a failed send. It holds the SMTP reply code or, for an API
// relay, the HTTP status (0 for network and TLS errors), never the reply
// text.
type SendError struct {
	Via  string // TypeSMTP (or empty) or TypeMailjetAPI
	Step string
	Code int
}

func (e *SendError) Error() string {
	via := e.Via
	if via == "" {
		via = TypeSMTP
	}
	if e.Code == 0 {
		return via + " " + e.Step + " failed"
	}
	return fmt.Sprintf("%s %s failed with code %d", via, e.Step, e.Code)
}

// Permanent reports whether an SMTP relay refused the recipient for good
// (5xx), so trying another relay will not help. API errors never count as
// permanent: their status does not say whether the recipient was the cause.
func (e *SendError) Permanent() bool {
	return (e.Via == "" || e.Via == TypeSMTP) && e.Code >= 500 && e.Code < 600 && e.Step == "rcpt"
}

func sendErr(step string, err error) error {
	var te *textproto.Error
	if errors.As(err, &te) {
		return &SendError{Step: step, Code: te.Code}
	}
	return &SendError{Step: step}
}

// Sender delivers through one relay. TLS and APIBase can be replaced in
// tests.
type Sender struct {
	Timeout time.Duration
	TLS     *tls.Config
	APIBase string
}

// DefaultTimeout bounds a whole delivery.
const DefaultTimeout = 30 * time.Second

// Send delivers msg to one recipient through r.
func (s Sender) Send(ctx context.Context, r Relay, from, to string, msg []byte) error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tlsConf := &tls.Config{ServerName: r.Host, MinVersion: tls.VersionTLS12}
	if s.TLS != nil {
		tlsConf = s.TLS.Clone()
		tlsConf.ServerName = r.Host
	}

	addr := net.JoinHostPort(r.Host, fmt.Sprint(r.Port))
	d := &net.Dialer{}
	var conn net.Conn
	var err error
	if r.implicitTLS() {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsConf}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return sendErr("connect", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	c, err := smtp.NewClient(conn, r.Host)
	if err != nil {
		return sendErr("greeting", err)
	}
	defer c.Close()
	if err := c.Hello(helloName(from)); err != nil {
		return sendErr("hello", err)
	}
	if !r.implicitTLS() {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return &SendError{Step: "starttls"}
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return sendErr("starttls", err)
		}
	}
	if r.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", r.Username, r.Password, r.Host)); err != nil {
			return sendErr("auth", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return sendErr("mail", err)
	}
	if err := c.Rcpt(to); err != nil {
		return sendErr("rcpt", err)
	}
	w, err := c.Data()
	if err != nil {
		return sendErr("data", err)
	}
	if _, err := w.Write(msg); err != nil {
		return sendErr("data", err)
	}
	if err := w.Close(); err != nil {
		return sendErr("data", err)
	}
	// The message was accepted; a failing QUIT does not undo that.
	_ = c.Quit()
	return nil
}

// helloName is the domain of the sender address, for EHLO.
func helloName(from string) string {
	if _, domain, ok := strings.Cut(from, "@"); ok {
		return domain
	}
	return "localhost"
}
