package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// Relay types: how a relay is reached.
const (
	TypeSMTP = "smtp" // the default
	// TypeMailjetAPI is Mailjet's Send API v3.1 over HTTPS, for servers
	// whose provider blocks outgoing SMTP. Username and password are the
	// API key and secret (Mailjet's SMTP credentials); host and port are
	// fixed (mailjetURL).
	TypeMailjetAPI = "mailjet-api"
)

// mailjetURL is the only address the API relay ever connects to; nothing
// in it comes from a client or the configuration.
const mailjetURL = "https://api.mailjet.com/v3.1/send"

// maxAPIResponse caps the response body that is read (and discarded).
const maxAPIResponse = 64 << 10

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
	url := mailjetURL
	if s.APIURL != "" {
		url = s.APIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
