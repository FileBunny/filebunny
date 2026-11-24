package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// uguuDomain is Uguu's domain. Uploads go to the domain itself; files are
// served from one-letter subdomains (seen: d, h, n), so the reference
// keeps the letter: "<letter>/<name>".
const uguuDomain = "uguu.se"

// uguuRef is the form of a reference: the file host's letter and the file
// name Uguu gave out (random letters and digits, plus the extension of the
// name we upload under). Only references of this form are stored or turned
// into a URL.
var uguuRef = regexp.MustCompile(`^[a-z]/[A-Za-z0-9]{6,32}\.bin$`)

// uguuHosts are the only hosts the adapter talks to: the domain and its
// one-letter subdomains.
func uguuHosts(domain string) []string {
	hosts := []string{domain}
	for c := 'a'; c <= 'z'; c++ {
		hosts = append(hosts, string(c)+"."+domain)
	}
	return hosts
}

// Uguu stores blobs at uguu.se, which chooses the file name and host
// itself and keeps every file for 3 hours. It has no way to delete a file: Delete does
// nothing, and a blob stays there until it expires. Anyone who knows the
// file name can read it; the names are random and only this server knows
// them.
type Uguu struct {
	domain string
	http   *http.Client
}

// uguuOptions are seams for tests; production uses the defaults.
type uguuOptions struct {
	domain string
	clientOptions
}

// NewUguu returns the adapter for uguu.se.
func NewUguu() *Uguu {
	return newUguu(uguuOptions{
		domain:        uguuDomain,
		clientOptions: publicClient(uguuHosts(uguuDomain)...),
	})
}

func newUguu(o uguuOptions) *Uguu {
	return &Uguu{domain: o.domain, http: newClient(o.clientOptions, nil)}
}

// Name implements Remote.
func (u *Uguu) Name() string { return "uguu" }

// MaxExpiry implements Remote. Uguu keeps a file for 3 hours from the
// upload; an hour less leaves room for a slow upload.
func (u *Uguu) MaxExpiry() time.Duration { return 2 * time.Hour }

// uguuReply is the JSON answer to an upload.
type uguuReply struct {
	Success bool `json:"success"`
	Files   []struct {
		URL  string `json:"url"`
		Size int64  `json:"size"`
	} `json:"files"`
}

// Upload implements Remote: one multipart field "files[]" named data.bin.
// Uguu takes no checksum; the reply must name a file of the right size on
// one of the file hosts.
func (u *Uguu) Upload(ctx context.Context, r io.Reader, size int64, _ [32]byte) (string, error) {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	if _, err := mw.CreateFormFile("files[]", "data.bin"); err != nil {
		return "", err
	}
	prefix := bytes.Clone(head.Bytes())
	head.Reset()
	if err := mw.Close(); err != nil {
		return "", err
	}
	body := io.MultiReader(bytes.NewReader(prefix), io.LimitReader(r, size), &head)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+u.domain+"/upload", body)
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(prefix)) + size + int64(head.Len())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := u.http.Do(req)
	if err != nil {
		return "", unavailable(err)
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: upload status %d", ErrUnavailable, resp.StatusCode)
	}
	var reply uguuReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReplySize)).Decode(&reply); err != nil {
		return "", fmt.Errorf("%w: unreadable reply", ErrUnavailable)
	}
	if !reply.Success || len(reply.Files) != 1 {
		return "", fmt.Errorf("%w: upload not accepted", ErrUnavailable)
	}
	if reply.Files[0].Size != size {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, errSize)
	}
	ref, ok := u.refFromURL(reply.Files[0].URL)
	if !ok {
		// Not logged: the URL would name the blob.
		return "", fmt.Errorf("%w: unexpected file URL", ErrUnavailable)
	}
	return ref, nil
}

// refFromURL accepts only https://<letter>.<domain>/<name> with a name of
// the expected form, nothing more, and returns "<letter>/<name>".
func (u *Uguu) refFromURL(raw string) (string, bool) {
	got, err := url.Parse(raw)
	if err != nil || got.Scheme != "https" || got.User != nil || got.RawQuery != "" || got.ForceQuery ||
		got.Fragment != "" || got.Opaque != "" || len(got.Path) < 2 || got.Path[0] != '/' {
		return "", false
	}
	letter, ok := strings.CutSuffix(got.Host, "."+u.domain)
	if !ok {
		return "", false
	}
	ref := letter + got.Path
	if !uguuRef.MatchString(ref) {
		return "", false
	}
	return ref, true
}

// fileURL turns a checked reference into its URL.
func (u *Uguu) fileURL(ref string) string {
	letter, name, _ := strings.Cut(ref, "/")
	return "https://" + letter + "." + u.domain + "/" + name
}

// Download implements Remote.
func (u *Uguu) Download(ctx context.Context, ref string, size int64) (io.ReadCloser, error) {
	if !uguuRef.MatchString(ref) {
		return nil, ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.fileURL(ref), nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, unavailable(err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		drain(resp.Body)
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		drain(resp.Body)
		return nil, fmt.Errorf("%w: download status %d", ErrUnavailable, resp.StatusCode)
	case isHTML(resp.Header.Get("Content-Type")):
		drain(resp.Body)
		return nil, fmt.Errorf("%w: got a web page instead of the file", ErrUnavailable)
	case resp.ContentLength >= 0 && resp.ContentLength != size:
		drain(resp.Body)
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, errSize)
	}
	return &exactReader{r: resp.Body, left: size}, nil
}

// Delete implements Remote. Uguu cannot delete a file; it expires 3 hours
// after the upload.
func (u *Uguu) Delete(context.Context, string) error { return nil }
