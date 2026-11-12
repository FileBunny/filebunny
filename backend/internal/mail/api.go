package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Relay types: how a relay is reached.
const (
	TypeSMTP = "smtp" // the default
	// TypeMailjetAPI is Mailjet's Send API v3.1 over HTTPS, for servers
	// whose provider blocks outgoing SMTP. Username and password are the
	// API key and secret (Mailjet's SMTP credentials); host and port are
	// fixed (mailjetBase).
	TypeMailjetAPI = "mailjet-api"
)

// mailjetBase is the only host the API relay ever connects to; nothing in
// its URLs comes from a client or the configuration.
const mailjetBase = "https://api.mailjet.com"

const (
	// maxAPIResponse caps the response body that is read (and discarded).
	maxAPIResponse = 64 << 10
	// maxContactsResponse caps one page of contacts (at most 1000).
	maxContactsResponse = 4 << 20
	// ContactsPage is the most contacts Mailjet returns at once.
	ContactsPage = 1000
)

// Deliver sends m to m.To through r, by the relay's type.
func (s Sender) Deliver(ctx context.Context, r Relay, m Message) error {
	switch r.kind() {
	case TypeMailjetAPI:
		return s.sendMailjet(ctx, r, m)
	default:
		return s.Send(ctx, r, m.From, m.To, m.Bytes())
	}
}

type mjAddress struct {
	Email string `json:"Email"`
	Name  string `json:"Name,omitempty"`
}

type mjMessage struct {
	From     mjAddress         `json:"From"`
	To       []mjAddress       `json:"To"`
	Subject  string            `json:"Subject"`
	TextPart string            `json:"TextPart"`
	Headers  map[string]string `json:"Headers,omitempty"`
	// Tracking rewrites links and adds a pixel: off for every message,
	// whatever the account's setting.
	TrackOpens  string `json:"TrackOpens"`
	TrackClicks string `json:"TrackClicks"`
}

// sendMailjet posts m to Mailjet's Send API. Errors hold the HTTP status
// only, never the response, which may quote the recipient.
func (s Sender) sendMailjet(ctx context.Context, r Relay, m Message) error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	headers := map[string]string{"Auto-Submitted": "auto-generated"}
	if m.Unsubscribe != "" {
		headers["List-Unsubscribe"] = "<" + m.Unsubscribe + ">"
		headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	body, err := json.Marshal(map[string][]mjMessage{"Messages": {{
		From:        mjAddress{Email: m.From, Name: "FileBunny"},
		To:          []mjAddress{{Email: m.To}},
		Subject:     m.Subject,
		TextPart:    m.Body,
		Headers:     headers,
		TrackOpens:  "disabled",
		TrackClicks: "disabled",
	}}})
	if err != nil {
		return &SendError{Via: TypeMailjetAPI, Step: "request"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiBase()+"/v3.1/send", bytes.NewReader(body))
	if err != nil {
		return &SendError{Via: TypeMailjetAPI, Step: "request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(r.Username, r.Password)

	resp, err := s.apiClient().Do(req)
	if err != nil {
		return &SendError{Via: TypeMailjetAPI, Step: "connect"}
	}
	defer resp.Body.Close()
	var result struct {
		Messages []struct {
			Status string `json:"Status"`
		} `json:"Messages"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponse)).Decode(&result)
	if resp.StatusCode != http.StatusOK {
		return &SendError{Via: TypeMailjetAPI, Step: "send", Code: resp.StatusCode}
	}
	if decodeErr != nil || len(result.Messages) != 1 || result.Messages[0].Status != "success" {
		return &SendError{Via: TypeMailjetAPI, Step: "response"}
	}
	return nil
}

func (s Sender) apiBase() string {
	if s.APIBase != "" {
		return s.APIBase
	}
	return mailjetBase
}

// Contact is a contact in the relay's account, as far as the contact
// cleanup needs it.
type Contact struct {
	ID        int64
	Email     string
	CreatedAt time.Time
	// Unsubscribed and SpamComplaint: the address asked for no more mail
	// at the relay (Mailjet's own unsubscribe link) or reported spam.
	Unsubscribed, SpamComplaint bool
}

// Anonymized reports whether a GDPR delete already anonymized the contact;
// Mailjet keeps it for 30 days with an address in the .invalid domain.
func (c Contact) Anonymized() bool { return strings.HasSuffix(strings.ToLower(c.Email), ".invalid") }

// flexInt is a number Mailjet sends as a number or as a string.
type flexInt int64

func (n *flexInt) UnmarshalJSON(b []byte) error {
	v, err := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64)
	*n = flexInt(v)
	return err
}

// MailjetContacts returns one page of the account's contacts (at most
// ContactsPage, from offset) and the total number.
func (s Sender) MailjetContacts(ctx context.Context, r Relay, offset int) ([]Contact, int, error) {
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout())
	defer cancel()
	url := fmt.Sprintf("%s/v3/REST/contact?Limit=%d&Offset=%d", s.apiBase(), ContactsPage, offset)
	resp, err := s.apiDo(ctx, r, http.MethodGet, url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, &SendError{Via: TypeMailjetAPI, Step: "contacts", Code: resp.StatusCode}
	}
	var page struct {
		Total flexInt `json:"Total"`
		Data  []struct {
			ID                flexInt `json:"ID"`
			Email             string  `json:"Email"`
			CreatedAt         string  `json:"CreatedAt"`
			UnsubscribedAt    string  `json:"UnsubscribedAt"`
			IsSpamComplaining bool    `json:"IsSpamComplaining"`
		} `json:"Data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxContactsResponse)).Decode(&page); err != nil {
		return nil, 0, &SendError{Via: TypeMailjetAPI, Step: "contacts"}
	}
	out := make([]Contact, 0, len(page.Data))
	for _, d := range page.Data {
		created, _ := time.Parse(time.RFC3339, d.CreatedAt) // zero if missing: never deleted
		out = append(out, Contact{ID: int64(d.ID), Email: d.Email, CreatedAt: created,
			Unsubscribed: d.UnsubscribedAt != "", SpamComplaint: d.IsSpamComplaining})
	}
	return out, int(page.Total), nil
}

// MailjetDeleteContact deletes a contact for good (GDPR delete): Mailjet
// anonymizes it at once and removes it after 30 days.
func (s Sender) MailjetDeleteContact(ctx context.Context, r Relay, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, s.apiTimeout())
	defer cancel()
	resp, err := s.apiDo(ctx, r, http.MethodDelete, fmt.Sprintf("%s/v4/contacts/%d", s.apiBase(), id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxAPIResponse))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &SendError{Via: TypeMailjetAPI, Step: "delete contact", Code: resp.StatusCode}
	}
	return nil
}

// IsRateLimited reports whether the API said "too many requests".
func IsRateLimited(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Via == TypeMailjetAPI && se.Code == http.StatusTooManyRequests
}

func (s Sender) apiTimeout() time.Duration {
	if s.Timeout != 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

func (s Sender) apiDo(ctx context.Context, r Relay, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, &SendError{Via: TypeMailjetAPI, Step: "request"}
	}
	req.SetBasicAuth(r.Username, r.Password)
	resp, err := s.apiClient().Do(req)
	if err != nil {
		return nil, &SendError{Via: TypeMailjetAPI, Step: "connect"}
	}
	return resp, nil
}

// apiClient follows no redirects, uses no proxy from the environment and
// needs TLS 1.2 or later with a verified certificate.
func (s Sender) apiClient() *http.Client {
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.TLS != nil {
		tlsConf = s.TLS.Clone()
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:       tlsConf,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect") },
	}
}
