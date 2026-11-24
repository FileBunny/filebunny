package storage

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

// filebinFile is the one file in each bin. Bins are named by the ref.
const filebinFile = "data.bin"

// Filebin stores blobs at filebin.net, one bin per blob. Downloads are
// redirected to the service's object storage, which is the only other host
// a request may go to.
//
// Filebin keeps a bin for about 7 days; MaxExpiry leaves a day of margin.
// Anyone who knows a bin's name can read or delete it; the names are random
// and only this server knows them.
//
// A download may first answer with a verification page that sets a cookie;
// as the API documentation says, the request is then repeated once with the
// cookie. Another page after that counts as ErrUnavailable.
type Filebin struct {
	base string
	http *http.Client
}

// filebinOptions are seams for tests; production uses the defaults.
type filebinOptions struct {
	base string
	clientOptions
}

// NewFilebin returns the adapter for filebin.net.
func NewFilebin() *Filebin {
	return newFilebin(filebinOptions{
		base:          "https://filebin.net",
		clientOptions: publicClient("filebin.net", "storage.filebin.net"),
	})
}

func newFilebin(o filebinOptions) *Filebin {
	apiHost := ""
	if base, err := url.Parse(o.base); err == nil {
		apiHost = base.Hostname()
	}
	jar, _ := cookiejar.New(nil) // never fails without options
	return &Filebin{base: o.base, http: newClient(o.clientOptions, hostJar{host: apiHost, jar: jar})}
}

// Name implements Remote.
func (f *Filebin) Name() string { return "filebin" }

// MaxExpiry implements Remote.
func (f *Filebin) MaxExpiry() time.Duration { return 6 * 24 * time.Hour }

func (f *Filebin) fileURL(ref string) string {
	return f.base + "/" + url.PathEscape(ref) + "/" + filebinFile
}

// Upload implements Remote.
func (f *Filebin) Upload(ctx context.Context, ref string, r io.Reader, size int64, sum [32]byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.fileURL(ref), r)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-SHA256", hex.EncodeToString(sum[:]))
	resp, err := f.http.Do(req)
	if err != nil {
		return unavailable(err)
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("%w: upload status %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// Download implements Remote.
func (f *Filebin) Download(ctx context.Context, ref string, size int64) (io.ReadCloser, error) {
	resp, err := f.get(ctx, ref)
	if err == nil && resp.StatusCode == http.StatusOK && isHTML(resp.Header.Get("Content-Type")) {
		// The verification page has set its cookie: repeat once.
		drain(resp.Body)
		resp, err = f.get(ctx, ref)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		drain(resp.Body)
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		drain(resp.Body)
		return nil, fmt.Errorf("%w: download status %d", ErrUnavailable, resp.StatusCode)
	case isHTML(resp.Header.Get("Content-Type")):
		// Still a verification page, not the blob.
		drain(resp.Body)
		return nil, fmt.Errorf("%w: got a web page instead of the file", ErrUnavailable)
	case resp.ContentLength >= 0 && resp.ContentLength != size:
		drain(resp.Body)
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, errSize)
	}
	return &exactReader{r: resp.Body, left: size}, nil
}

func (f *Filebin) get(ctx context.Context, ref string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.fileURL(ref), nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, unavailable(err)
	}
	return resp, nil
}

// hostJar keeps cookies for the API host only, so none go to the object
// storage host.
type hostJar struct {
	host string
	jar  http.CookieJar
}

func (h hostJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if u.Hostname() == h.host {
		h.jar.SetCookies(u, cookies)
	}
}

func (h hostJar) Cookies(u *url.URL) []*http.Cookie {
	if u.Hostname() != h.host {
		return nil
	}
	return h.jar.Cookies(u)
}

// Delete implements Remote. It deletes the whole bin.
func (f *Filebin) Delete(ctx context.Context, ref string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, f.base+"/"+url.PathEscape(ref), nil)
	if err != nil {
		return err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return unavailable(err)
	}
	defer drain(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	}
	return fmt.Errorf("%w: delete status %d", ErrUnavailable, resp.StatusCode)
}
