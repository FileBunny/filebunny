package wkd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseAddress(t *testing.T) {
	for _, s := range []string{
		"alice@example.org",
		"Joe.Doe@Example.ORG",
		"a+tag@sub.example.co.uk",
		"x_y-z!#$%&'*/=?^`{|}~@example.org",
		"user@xn--bcher-kva.example",
		"u@a1.b2",
		strings.Repeat("a", 64) + "@example.org",
	} {
		if _, err := ParseAddress(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{
		"",
		"alice",
		"alice@",
		"@example.org",
		"a@b@example.org",
		"alice@localhost",
		"alice@example.org.",
		"alice@.example.org",
		"alice@example..org",
		"alice@-example.org",
		"alice@example-.org",
		"alice@exa_mple.org",
		"alice@example.org:8443",
		"alice@[127.0.0.1]",
		"alice@127.0.0.1",
		"alice@10.0.0.1",
		"alice@[::1]",
		"alice@bücher.example",
		"alice@example.org/evil",
		"alice@example.org?x",
		"alice@example.org#x",
		"alice@exam ple.org",
		"ali ce@example.org",
		"\"alice\"@example.org",
		".alice@example.org",
		"alice.@example.org",
		"al..ice@example.org",
		"alice\n@example.org",
		"álice@example.org",
		strings.Repeat("a", 65) + "@example.org",
		"a@" + strings.Repeat("a", 64) + ".org",
		"a@" + strings.Repeat("abcdefghi.", 25) + "org",
	} {
		if _, err := ParseAddress(s); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("%q: accepted", s)
		}
	}
}

// Example from draft-koch-openpgp-webkey-service, section 3.1.
func TestURLs(t *testing.T) {
	a, err := ParseAddress("Joe.Doe@Example.ORG")
	if err != nil {
		t.Fatal(err)
	}
	if h := a.Hash(); h != "iy9q119eutrkn8s1mk4r39qejnbu3n5q" {
		t.Errorf("hash %q", h)
	}
	if u := a.AdvancedURL(); u != "https://openpgpkey.example.org/.well-known/openpgpkey/example.org/hu/iy9q119eutrkn8s1mk4r39qejnbu3n5q?l=Joe.Doe" {
		t.Errorf("advanced %q", u)
	}
	if u := a.DirectURL(); u != "https://example.org/.well-known/openpgpkey/hu/iy9q119eutrkn8s1mk4r39qejnbu3n5q?l=Joe.Doe" {
		t.Errorf("direct %q", u)
	}
	b, _ := ParseAddress("a+b&c=d@example.org")
	if !strings.HasSuffix(b.DirectURL(), "?l=a%2Bb%26c%3Dd") {
		t.Errorf("local part not escaped: %q", b.DirectURL())
	}
}

func TestAllowed(t *testing.T) {
	for _, s := range []string{"93.184.215.14", "1.1.1.1", "2a00:1450:4001:80b::200e", "2606:4700::1111"} {
		if !Allowed(netip.MustParseAddr(s)) {
			t.Errorf("%s blocked", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "127.255.0.1", "::1", // loopback
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fc00::1", "fd12:3456::1", // private
		"169.254.169.254", "169.254.1.1", "fe80::1", "fe80::1%eth0", // link-local, metadata
		"100.64.0.1", "100.127.255.254", // CGNAT
		"0.0.0.0", "0.1.2.3", "::", // unspecified, this network
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff0e::1", // multicast
		"255.255.255.255", "240.0.0.1", // broadcast, reserved
		"192.0.2.1", "198.51.100.1", "203.0.113.1", "2001:db8::1", "3fff::1", // documentation
		"198.18.0.1", "198.19.255.255", "2001:2::1", // benchmarking
		"192.0.0.8", "192.88.99.1", // IETF, 6to4 relay
		"::ffff:127.0.0.1", "::ffff:8.8.8.8", "::127.0.0.1", // IPv4-mapped/compatible
		"64:ff9b::a00:1", "64:ff9b:1::1", // NAT64
		"2002:a00:1::1", "2002:808:808::1", // 6to4
		"2001:0:4136:e378::1", // Teredo
		"fec0::1", "100::1",   // site-local, discard
	} {
		if Allowed(netip.MustParseAddr(s)) {
			t.Errorf("%s allowed", s)
		}
	}
	if Allowed(netip.Addr{}) {
		t.Error("zero address allowed")
	}
}

// testEnv is a TLS server standing in for any domain. DNS is a map: hosts
// missing from it are NXDOMAIN.
type testEnv struct {
	srv  *httptest.Server
	hits atomic.Int32
	opts options
}

func newTestEnv(t *testing.T, h http.HandlerFunc, hosts ...string) *testEnv {
	t.Helper()
	e := &testEnv{}
	e.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(e.srv.Close)
	_, port, _ := net.SplitHostPort(e.srv.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(e.srv.Certificate())
	known := map[string]bool{}
	for _, h := range hosts {
		known[h] = true
	}
	e.opts = options{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if !known[host] {
				return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		port:    port,
		allowed: func(a netip.Addr) bool { return a.IsLoopback() },
		// The test certificate is for example.com; check against that name.
		tls: &tls.Config{RootCAs: pool, ServerName: "example.com"},
	}
	return e
}

func (e *testEnv) lookup(t *testing.T, addr string) ([]byte, error) {
	t.Helper()
	a, err := ParseAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	key, err := newClient(e.opts).Lookup(t.Context(), a)
	if err != nil && strings.Contains(err.Error(), "example") {
		t.Errorf("error leaks the domain: %v", err)
	}
	return key, err
}

var testKey = []byte("\x99\x00\x33binary key bytes")

func TestLookupAdvanced(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "openpgpkey.example.org" || r.URL.Path != "/.well-known/openpgpkey/example.org/hu/iy9q119eutrkn8s1mk4r39qejnbu3n5q" || r.URL.Query().Get("l") != "Joe.Doe" {
			t.Errorf("unexpected request %s %s", r.Host, r.URL)
		}
		w.Write(testKey)
	}, "openpgpkey.example.org", "example.org")
	key, err := e.lookup(t, "Joe.Doe@example.org")
	if err != nil || !bytes.Equal(key, testKey) {
		t.Fatalf("got %q, %v", key, err)
	}
}

func TestLookupFallsBackToDirectOnNXDOMAIN(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.org" || r.URL.Path != "/.well-known/openpgpkey/hu/iy9q119eutrkn8s1mk4r39qejnbu3n5q" {
			t.Errorf("unexpected request %s %s", r.Host, r.URL)
		}
		w.Write(testKey)
	}, "example.org")
	key, err := e.lookup(t, "joe.doe@example.org")
	if err != nil || !bytes.Equal(key, testKey) {
		t.Fatalf("got %q, %v", key, err)
	}
}

// A 404 on the advanced method is an answer: the direct method is not
// tried.
func TestLookupNoFallbackOnNotFound(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "openpgpkey.example.org" {
			t.Errorf("direct method tried: %s", r.Host)
		}
		http.NotFound(w, r)
	}, "openpgpkey.example.org", "example.org")
	if _, err := e.lookup(t, "alice@example.org"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err %v, want ErrNotFound", err)
	}
	if e.hits.Load() != 1 {
		t.Errorf("%d requests, want 1", e.hits.Load())
	}
}

// Only NXDOMAIN falls back; another DNS failure does not.
func TestLookupNoFallbackOnOtherDNSError(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) { w.Write(testKey) }, "example.org")
	e.opts.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "openpgpkey.example.org" {
			return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	_, err := e.lookup(t, "alice@example.org")
	if !errors.Is(err, ErrUpstream) || Reason(err) != "dns" {
		t.Errorf("err %v (%s), want upstream dns", err, Reason(err))
	}
	if e.hits.Load() != 0 {
		t.Errorf("direct method tried")
	}
}

func TestLookupEmptyIsNotFound(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {}, "openpgpkey.example.org")
	if _, err := e.lookup(t, "alice@example.org"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err %v, want ErrNotFound", err)
	}
}

func TestLookupUpstreamFailures(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		reason  string
	}{
		"server error": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "upstream"},
		"redirect": {func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/elsewhere" {
				t.Error("redirect followed")
			}
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}, "redirect"},
		"redirect to internal host": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusMovedPermanently)
		}, "redirect"},
		"too large": {func(w http.ResponseWriter, r *http.Request) {
			w.Write(make([]byte, MaxKeySize+1))
		}, "too large"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t, c.handler, "openpgpkey.example.org")
			_, err := e.lookup(t, "alice@example.org")
			if !errors.Is(err, ErrUpstream) || Reason(err) != c.reason {
				t.Errorf("err %v (%s), want upstream %s", err, Reason(err), c.reason)
			}
		})
	}
}

func TestLookupMaxSizeAllowed(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxKeySize))
	}, "openpgpkey.example.org")
	if key, err := e.lookup(t, "alice@example.org"); err != nil || len(key) != MaxKeySize {
		t.Errorf("len %d, err %v", len(key), err)
	}
}

func TestLookupTimeout(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, "openpgpkey.example.org")
	a, _ := ParseAddress("alice@example.org")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newClient(e.opts).Lookup(ctx, a)
	if !errors.Is(err, ErrUpstream) || Reason(err) != "timeout" {
		t.Errorf("err %v (%s), want upstream timeout", err, Reason(err))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

// With the production policy, a domain that resolves to loopback is never
// connected to.
func TestLookupBlocksInternalAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) { w.Write(testKey) }, "openpgpkey.example.org")
			e.opts.allowed = Allowed
			e.opts.lookup = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr(ip)}, nil
			}
			_, err := e.lookup(t, "alice@example.org")
			if !errors.Is(err, ErrUpstream) || Reason(err) != "blocked address" {
				t.Errorf("err %v (%s), want blocked", err, Reason(err))
			}
			if e.hits.Load() != 0 {
				t.Error("server reached")
			}
		})
	}
}

// The dialer checks the address it really connects to, not just what DNS
// said earlier (DNS rebinding, or any gap between check and dial).
func TestDialerControlChecksConnectedAddress(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) { w.Write(testKey) }, "openpgpkey.example.org")
	var calls atomic.Int32
	e.opts.allowed = func(netip.Addr) bool { return calls.Add(1) == 1 } // only the pre-dial check passes
	_, err := e.lookup(t, "alice@example.org")
	if Reason(err) != "blocked address" || e.hits.Load() != 0 {
		t.Errorf("err %v (%s), hits %d", err, Reason(err), e.hits.Load())
	}
}

func TestDialerControl(t *testing.T) {
	o := defaultOptions()
	for addr, ok := range map[string]bool{
		"93.184.215.14:443":     true,
		"[2606:4700::1]:443":    true,
		"93.184.215.14:80":      false,
		"93.184.215.14:8443":    false,
		"127.0.0.1:443":         false,
		"169.254.169.254:443":   false,
		"[::ffff:10.0.0.1]:443": false,
		"not an address":        false,
	} {
		if err := o.control("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: err %v", addr, err)
		}
	}
}

func TestTransportSettings(t *testing.T) {
	c := NewClient()
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || c.http.CheckRedirect == nil || c.http.Timeout != totalTimeout {
		t.Errorf("unsafe transport: %+v", tr)
	}
}

func TestLookupVerifiesTLS(t *testing.T) {
	e := newTestEnv(t, func(w http.ResponseWriter, r *http.Request) { w.Write(testKey) }, "openpgpkey.example.org")
	e.opts.tls = nil // system roots: the test CA is unknown
	_, err := e.lookup(t, "alice@example.org")
	if !errors.Is(err, ErrUpstream) || Reason(err) != "tls" {
		t.Errorf("err %v (%s), want tls failure", err, Reason(err))
	}
}

func TestZbase32(t *testing.T) {
	for in, want := range map[string]string{"": "", "\x00": "yy", "\xff": "9h"} {
		if got := zbase32([]byte(in)); got != want {
			t.Errorf("zbase32(%x) = %q, want %q", in, got, want)
		}
	}
}
