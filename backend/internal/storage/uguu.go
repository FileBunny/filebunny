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
	"time"
)

// uguuFileHost serves the uploaded files.
const uguuFileHost = "n.uguu.se"

// uguuName is the form of the file names Uguu gives out (random letters
// and digits, plus the extension of the name we upload under). Only names
// of this form are stored or turned into a URL.
var uguuName = regexp.MustCompile(`^[A-Za-z0-9]{6,32}\.bin$`)

// Uguu stores blobs at uguu.se, which chooses the file name itself and
// keeps every file for 3 hours. It has no way to delete a file: Delete does
// nothing, and a blob stays there until it expires. Anyone who knows the
// file name can read it; the names are random and only this server knows
// them.
type Uguu struct {
	base  string // the upload host
	files string // the file host
	http  *http.Client
}

// uguuOptions are seams for tests; production uses the defaults.
type uguuOptions struct {
	base, files string
	clientOptions
}

// NewUguu returns the adapter for uguu.se.
func NewUguu() *Uguu {
	return newUguu(uguuOptions{
		base:          "https://uguu.se",
		files:         "https://" + uguuFileHost,
		clientOptions: publicClient("uguu.se", uguuFileHost),
	})
}

func newUguu(o uguuOptions) *Uguu {
	return &Uguu{base: o.base, files: o.files, http: newClient(o.clientOptions, nil)}
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
// the file host.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.base+"/upload", body)
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

// refFromURL accepts only https://<file host>/<name> with a name of the
// expected form, nothing more.
func (u *Uguu) refFromURL(raw string) (string, bool) {
	want, err := url.Parse(u.files)
	if err != nil {
		return "", false
	}
	got, err := url.Parse(raw)
	if err != nil || got.Scheme != "https" || got.Host != want.Host || got.User != nil ||
		got.RawQuery != "" || got.Fragment != "" || got.Opaque != "" || len(got.Path) < 2 || got.Path[0] != '/' {
		return "", false
	}
	name := got.Path[1:]
	if !uguuName.MatchString(name) {
		return "", false
	}
	return name, true
}

// Download implements Remote.
func (u *Uguu) Download(ctx context.Context, ref string, size int64) (io.ReadCloser, error) {
	if !uguuName.MatchString(ref) {
		return nil, ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.files+"/"+ref, nil)
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
