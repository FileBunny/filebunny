package storage

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakeUguu imitates uguu.se (uploads) and n.uguu.se (files) on one test
// server.
type fakeUguu struct {
	mu    sync.Mutex
	blobs map[string][]byte
	next  int
	// url overrides the file URL in the reply ("%s" is the name).
	url string
	// size overrides the size in the reply.
	size int64
	// fail answers uploads with success false.
	fail bool
	// html makes downloads answer with a web page.
	html bool
	// big makes the upload reply larger than any reply may be.
	big bool
	// field and filename of the last upload, and its Content-Length.
	field, filename string
	length          int64
}

func (f *fakeUguu) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Host == "uguu.se" && r.Method == http.MethodPost && r.URL.Path == "/upload":
		f.length = r.ContentLength
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		f.field, f.filename = part.FormName(), part.FileName()
		data, _ := io.ReadAll(part)
		f.next++
		name := fmt.Sprintf("AbCdEf%06d.bin", f.next)
		f.blobs[name] = data
		size := int64(len(data))
		if f.size != 0 {
			size = f.size
		}
		u := "https://n.uguu.se/" + name
		if f.url != "" {
			u = fmt.Sprintf(f.url, name)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		if f.big {
			io.WriteString(w, `{"pad":"`+strings.Repeat("x", maxReplySize)+`"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": !f.fail,
			"files":   []map[string]any{{"hash": "0123456789abcdef", "filename": name, "url": u, "size": size, "dupe": false}},
		})
	case r.Host == "n.uguu.se" && r.Method == http.MethodGet:
		if f.html {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html>gone</html>")
			return
		}
		blob, ok := f.blobs[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(blob)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newTestUguu(t *testing.T, change ...func(*uguuOptions)) (*Uguu, *fakeUguu) {
	t.Helper()
	fake := &fakeUguu{blobs: map[string][]byte{}}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	o := uguuOptions{
		base:          "https://uguu.se",
		files:         "https://n.uguu.se",
		clientOptions: testClient(srv, "uguu.se", "n.uguu.se"),
	}
	for _, c := range change {
		c(&o)
	}
	return newUguu(o), fake
}

func uguuUpload(t *testing.T, u *Uguu, body string) (string, error) {
	return u.Upload(t.Context(), strings.NewReader(body), int64(len(body)), sha256.Sum256([]byte(body)))
}

func TestUguuRoundTrip(t *testing.T) {
	u, fake := newTestUguu(t)
	ref, err := uguuUpload(t, u, "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	if !uguuName.MatchString(ref) {
		t.Errorf("ref %q", ref)
	}
	if fake.field != "files[]" || fake.filename != "data.bin" {
		t.Errorf("uploaded as field %q, file %q", fake.field, fake.filename)
	}
	if fake.length <= int64(len("ciphertext")) {
		t.Errorf("Content-Length %d, want the whole form", fake.length)
	}

	rc, err := u.Download(t.Context(), ref, int64(len("ciphertext")))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(data) != "ciphertext" {
		t.Fatalf("download %q, %v", data, err)
	}

	// Uguu cannot delete: Delete succeeds and the file stays.
	if err := u.Delete(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	delete(fake.blobs, ref) // as when it expires
	if _, err := u.Download(t.Context(), ref, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("after expiry: %v, want ErrNotFound", err)
	}
}

// The reply decides the reference, so it is checked strictly: only a file
// of the right size, as https://n.uguu.se/<name>.
func TestUguuReplyChecked(t *testing.T) {
	cases := map[string]func(*fakeUguu){
		"other host":     func(f *fakeUguu) { f.url = "https://attacker.example/%s" },
		"http":           func(f *fakeUguu) { f.url = "http://n.uguu.se/%s" },
		"user info":      func(f *fakeUguu) { f.url = "https://x@n.uguu.se/%s" },
		"query":          func(f *fakeUguu) { f.url = "https://n.uguu.se/%s?a=b" },
		"path":           func(f *fakeUguu) { f.url = "https://n.uguu.se/../%s" },
		"sub directory":  func(f *fakeUguu) { f.url = "https://n.uguu.se/x/%s" },
		"other name":     func(f *fakeUguu) { f.url = "https://n.uguu.se/%s.html" },
		"wrong size":     func(f *fakeUguu) { f.size = 3 },
		"not accepted":   func(f *fakeUguu) { f.fail = true },
		"reply too long": func(f *fakeUguu) { f.big = true },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			u, fake := newTestUguu(t)
			change(fake)
			if _, err := uguuUpload(t, u, "ciphertext"); !errors.Is(err, ErrUnavailable) {
				t.Errorf("err %v, want ErrUnavailable", err)
			}
		})
	}
}

// A stored reference that is not a Uguu name never becomes a URL.
func TestUguuInvalidReference(t *testing.T) {
	u, _ := newTestUguu(t)
	for _, ref := range []string{"", "../x.bin", "a/b.bin", "abcdef.html", "abcdefgh.bin?x", strings.Repeat("a", 40) + ".bin"} {
		if _, err := u.Download(t.Context(), ref, 1); !errors.Is(err, ErrNotFound) {
			t.Errorf("ref %q: %v, want ErrNotFound", ref, err)
		}
	}
}

func TestUguuDownloadChecked(t *testing.T) {
	u, fake := newTestUguu(t)
	ref, err := uguuUpload(t, u, "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Download(t.Context(), ref, 11); !errors.Is(err, errSize) {
		t.Errorf("wrong size: %v, want errSize", err)
	}
	fake.html = true
	if _, err := u.Download(t.Context(), ref, 10); !errors.Is(err, ErrUnavailable) {
		t.Errorf("web page: %v, want ErrUnavailable", err)
	}
}

func TestUguuBlockedAddress(t *testing.T) {
	u, _ := newTestUguu(t, func(o *uguuOptions) {
		o.allowed = func(a netip.Addr) bool { return !a.IsLoopback() }
	})
	if _, err := uguuUpload(t, u, "x"); !errors.Is(err, errBlocked) {
		t.Errorf("err %v, want errBlocked", err)
	}
}

// Errors never contain the file name (it ends up in logs).
func TestUguuErrorsHideTheReference(t *testing.T) {
	u, fake := newTestUguu(t)
	ref, err := uguuUpload(t, u, "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	fake.html = true
	if _, err := u.Download(t.Context(), ref, 10); err == nil || strings.Contains(err.Error(), ref) {
		t.Errorf("error %q contains the reference", err)
	}
	fake.url = "https://attacker.example/%s"
	if _, err := uguuUpload(t, u, "ciphertext"); err == nil || strings.Contains(err.Error(), "AbCdEf") {
		t.Errorf("error %q contains the name", err)
	}
}
