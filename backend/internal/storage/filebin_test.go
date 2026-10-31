package storage

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakeFilebin imitates filebin.net and its object storage on one test
// server: POST and DELETE on filebin.net, GET redirects to storage.
type fakeFilebin struct {
	mu    sync.Mutex
	blobs map[string][]byte
	// html makes downloads answer with a verification page.
	html bool
	// redirectTo overrides the storage host of the download redirect.
	redirectTo string
	// extra is appended to downloaded blobs.
	extra string
	sums  []string
}

func (f *fakeFilebin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Host == "storage.filebin.net" && r.Method == http.MethodGet:
		blob, ok := f.blobs[strings.TrimPrefix(r.URL.Path, "/s")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(append(blob, f.extra...))
	case r.Method == http.MethodPost:
		data, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(data)
		if r.Header.Get("Content-SHA256") != hex.EncodeToString(sum[:]) {
			http.Error(w, "checksum mismatch", http.StatusBadRequest)
			return
		}
		f.sums = append(f.sums, r.Header.Get("Content-SHA256"))
		f.blobs[r.URL.Path] = data
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"bin":{}}`)
	case r.Method == http.MethodGet:
		if f.html {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html>verify</html>")
			return
		}
		if _, ok := f.blobs[r.URL.Path]; !ok {
			http.NotFound(w, r)
			return
		}
		host := "storage.filebin.net"
		if f.redirectTo != "" {
			host = f.redirectTo
		}
		http.Redirect(w, r, "https://"+host+"/s"+r.URL.Path, http.StatusFound)
	case r.Method == http.MethodDelete:
		for k := range f.blobs {
			if strings.HasPrefix(k, r.URL.Path+"/") {
				delete(f.blobs, k)
			}
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newTestFilebin(t *testing.T, change ...func(*filebinOptions)) (*Filebin, *fakeFilebin) {
	t.Helper()
	fake := &fakeFilebin{blobs: map[string][]byte{}}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	o := filebinOptions{
		base:  "https://filebin.net",
		hosts: map[string]bool{"filebin.net": true, "storage.filebin.net": true},
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		port:    port,
		allowed: func(netip.Addr) bool { return true },
		// The test certificate is for example.com; check against that name.
		tls: &tls.Config{RootCAs: pool, ServerName: "example.com"},
	}
	for _, c := range change {
		c(&o)
	}
	return newFilebin(o), fake
}

func upload(t *testing.T, fb *Filebin, ref, body string) {
	t.Helper()
	if err := fb.Upload(t.Context(), ref, strings.NewReader(body), int64(len(body)), sha256.Sum256([]byte(body))); err != nil {
		t.Fatal(err)
	}
}

func TestFilebinRoundTrip(t *testing.T) {
	fb, fake := newTestFilebin(t)
	ref := NewRef()
	upload(t, fb, ref, "ciphertext")
	if len(fake.sums) != 1 {
		t.Fatal("no checksum sent")
	}

	rc, err := fb.Download(t.Context(), ref, int64(len("ciphertext")))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(data) != "ciphertext" {
		t.Fatalf("download %q, %v", data, err)
	}

	if err := fb.Delete(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if _, err := fb.Download(t.Context(), ref, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v, want ErrNotFound", err)
	}
	// Deleting what is gone is fine.
	if err := fb.Delete(t.Context(), ref); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestFilebinChecksumMismatch(t *testing.T) {
	fb, _ := newTestFilebin(t)
	err := fb.Upload(t.Context(), NewRef(), strings.NewReader("data"), 4, sha256.Sum256([]byte("other")))
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want ErrUnavailable", err)
	}
}

// A verification page is never bypassed: the service is unavailable.
func TestFilebinVerificationPage(t *testing.T) {
	fb, fake := newTestFilebin(t)
	ref := NewRef()
	upload(t, fb, ref, "ciphertext")
	fake.html = true
	if _, err := fb.Download(t.Context(), ref, 10); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err %v, want ErrUnavailable", err)
	}
}

func TestFilebinWrongSize(t *testing.T) {
	fb, fake := newTestFilebin(t)
	ref := NewRef()
	upload(t, fb, ref, "ciphertext")

	// Longer than stored: refused before streaming (Content-Length).
	fake.extra = "padding"
	if _, err := fb.Download(t.Context(), ref, 10); !errors.Is(err, errSize) {
		t.Errorf("longer: %v, want errSize", err)
	}
	// Shorter than recorded.
	fake.extra = ""
	if _, err := fb.Download(t.Context(), ref, 11); !errors.Is(err, errSize) {
		t.Errorf("shorter: %v, want errSize", err)
	}
}

// Redirects only go to the fixed hosts, never anywhere the service says.
func TestFilebinRedirectToOtherHostRefused(t *testing.T) {
	fb, fake := newTestFilebin(t)
	ref := NewRef()
	upload(t, fb, ref, "ciphertext")
	fake.redirectTo = "attacker.example"
	_, err := fb.Download(t.Context(), ref, 10)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, errRedirect) {
		t.Errorf("err %v, want a refused redirect", err)
	}
}

func TestFilebinBlockedAddress(t *testing.T) {
	fb, _ := newTestFilebin(t, func(o *filebinOptions) {
		o.allowed = func(a netip.Addr) bool { return !a.IsLoopback() }
	})
	err := fb.Upload(t.Context(), NewRef(), strings.NewReader("x"), 1, sha256.Sum256([]byte("x")))
	if !errors.Is(err, errBlocked) {
		t.Errorf("err %v, want errBlocked", err)
	}
}

// Errors never contain the blob's reference (it ends up in logs).
func TestFilebinErrorsHideTheReference(t *testing.T) {
	fb, fake := newTestFilebin(t)
	ref := NewRef()
	upload(t, fb, ref, "ciphertext")
	fake.redirectTo = "attacker.example"
	_, err := fb.Download(t.Context(), ref, 10)
	if err == nil || strings.Contains(err.Error(), ref) {
		t.Errorf("error %q contains the reference", err)
	}
}

func TestNewRef(t *testing.T) {
	a, b := NewRef(), NewRef()
	if len(a) != 32 || a == b || strings.ToLower(a) != a {
		t.Errorf("refs %q, %q", a, b)
	}
}
