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

// fakeUguu imitates uguu.se (uploads) and its one-letter file hosts on
// one test server; like the real one, it spreads files over several hosts.
type fakeUguu struct {
	mu    sync.Mutex
	blobs map[string][]byte
	next  int
	// url overrides the file URL in the reply ("%s" is the name).
	url string
	// hosts counts the file hosts used.
	hosts map[string]int
	// size overrides the size in the reply.
	size int64
	// fail answers uploads with success false.
	fail bool
	// html makes downloads answer with a web page.
	html bool
	// redirect makes downloads redirect to this origin, same path.
	redirect string
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
		host := []string{"d", "h", "n"}[f.next%3] + ".uguu.se"
		f.blobs[host+"/"+name] = data
		size := int64(len(data))
		if f.size != 0 {
			size = f.size
		}
		u := "https://" + host + "/" + name
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
	case strings.HasSuffix(r.Host, ".uguu.se") && r.Method == http.MethodGet:
		f.hosts[r.Host]++
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect+r.URL.Path, http.StatusFound)
			return
		}
		if f.html {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html>gone</html>")
			return
		}
		blob, ok := f.blobs[r.Host+r.URL.Path]
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
	fake := &fakeUguu{blobs: map[string][]byte{}, hosts: map[string]int{}}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	o := uguuOptions{
		domain:        "uguu.se",
		clientOptions: testClient(srv, uguuHosts("uguu.se")...),
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
	if !uguuRef.MatchString(ref) {
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
	clear(fake.blobs) // as when it expires
	if _, err := u.Download(t.Context(), ref, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("after expiry: %v, want ErrNotFound", err)
	}
}

// Files on different hosts all come back.
func TestUguuFileHosts(t *testing.T) {
	u, fake := newTestUguu(t)
	for i := range 3 {
		body := fmt.Sprintf("ciphertext %d", i)
		ref, err := uguuUpload(t, u, body)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := u.Download(t.Context(), ref, int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if string(data) != body {
			t.Errorf("download %q, want %q", data, body)
		}
	}
	if len(fake.hosts) != 3 {
		t.Errorf("file hosts used: %v, want 3", fake.hosts)
	}
}

// The reply decides the reference, so it is checked strictly: only a file
// of the right size, as https://<letter>.uguu.se/<name>.
func TestUguuReplyChecked(t *testing.T) {
	cases := map[string]func(*fakeUguu){
		"other host":     func(f *fakeUguu) { f.url = "https://attacker.example/%s" },
		"other domain":   func(f *fakeUguu) { f.url = "https://n.uguu.se.attacker.example/%s" },
		"two letters":    func(f *fakeUguu) { f.url = "https://nn.uguu.se/%s" },
		"deeper host":    func(f *fakeUguu) { f.url = "https://a.n.uguu.se/%s" },
		"upload host":    func(f *fakeUguu) { f.url = "https://uguu.se/%s" },
		"port":           func(f *fakeUguu) { f.url = "https://n.uguu.se:8443/%s" },
		"empty query":    func(f *fakeUguu) { f.url = "https://n.uguu.se/%s?" },
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
	for _, ref := range []string{"", "abcdefgh.bin", "../x.bin", "n/../abcdefgh.bin", "nn/abcdefgh.bin", "N/abcdefgh.bin",
		"n/abcdef.html", "n/abcdefgh.bin?x", "n/" + strings.Repeat("a", 40) + ".bin"} {
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

// Errors never contain the file name (it ends up in logs), also when the
// HTTP client fails with a *url.Error, which names the URL.
func TestUguuErrorsHideTheReference(t *testing.T) {
	u, fake := newTestUguu(t)
	ref, err := uguuUpload(t, u, "ciphertext")
	if err != nil {
		t.Fatal(err)
	}
	// The URL is https://<letter>.uguu.se/<name>: look for the name.
	_, name, _ := strings.Cut(ref, "/")
	fake.html = true
	if _, err := u.Download(t.Context(), ref, 10); err == nil || strings.Contains(err.Error(), name) {
		t.Errorf("web page: error %q contains the name", err)
	}
	fake.html = false
	fake.redirect = "https://attacker.example"
	if _, err := u.Download(t.Context(), ref, 10); !errors.Is(err, errRedirect) || strings.Contains(err.Error(), name) {
		t.Errorf("refused redirect: error %q, want errRedirect without the name", err)
	}
	fake.redirect = ""
	fake.url = "https://attacker.example/%s"
	if _, err := uguuUpload(t, u, "ciphertext"); err == nil || strings.Contains(err.Error(), "AbCdEf") {
		t.Errorf("error %q contains the name", err)
	}
}
