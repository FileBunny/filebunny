package wkd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

// Limits for one lookup.
const (
	// MaxKeySize caps a WKD response. Real keys are a few KiB.
	MaxKeySize    = 1 << 20
	totalTimeout  = 10 * time.Second
	dialTimeout   = 5 * time.Second
	tlsTimeout    = 5 * time.Second
	headerTimeout = 5 * time.Second
)

var (
	// ErrNotFound means the domain publishes no key for the address.
	ErrNotFound = errors.New("no key published")
	// ErrUpstream means the lookup failed: blocked address, DNS, TLS,
	// timeout, redirect, unexpected status or an oversized response.
	ErrUpstream = errors.New("lookup failed")
)

// Client looks up keys over HTTPS with SSRF protection:
//
//   - only https on port 443, URLs built from a validated address only;
//   - no proxy, no redirects, TLS verification on;
//   - every address DNS returns is checked before dialing, and again in
//     the dialer's Control function on the address actually connected to,
//     which also covers DNS rebinding;
//   - short timeouts and a 1 MiB response cap.
type Client struct {
	http *http.Client
}

// options are seams for tests; production uses defaultOptions.
type options struct {
	// lookup resolves a host name.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// port is dialed instead of the URL's port (always 443 in production).
	port string
	// allowed decides which addresses may be connected to.
	allowed func(netip.Addr) bool
	// tls is the client TLS configuration (test CAs).
	tls *tls.Config
}

func defaultOptions() options {
	return options{
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			// LookupNetIP may return IPv4 addresses in IPv4-mapped form.
			for i := range ips {
				ips[i] = ips[i].Unmap()
			}
			return ips, err
		},
		port:    "443",
		allowed: Allowed,
	}
}

// NewClient returns a client for public WKD servers.
func NewClient() *Client {
	return newClient(defaultOptions())
}

func newClient(o options) *Client {
	dialer := &net.Dialer{Timeout: dialTimeout, Control: o.control}
	tlsConfig := o.tls
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := o.lookup(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error = errBlocked
			for _, ip := range ips {
				if !o.allowed(ip) {
					continue
				}
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), o.port))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    tlsTimeout,
		ResponseHeaderTimeout:  headerTimeout,
		MaxResponseHeaderBytes: 16 << 10,
		DisableCompression:     true,
		// Every lookup goes to a different domain; keep nothing open.
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
	}
	return &Client{http: &http.Client{
		Transport: transport,
		Timeout:   totalTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}}
}

// control is the dialer's last line of defence: it checks the address the
// socket really connects to.
func (o options) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !o.allowed(ap.Addr()) || ap.Port() != parsePort(o.port) {
		return errBlocked
	}
	return nil
}

func parsePort(s string) uint16 {
	p, _ := strconv.ParseUint(s, 10, 16)
	return uint16(p)
}

var (
	errBlocked  = errors.New("address not allowed")
	errRedirect = errors.New("redirect refused")
	errTooLarge = errors.New("response too large")
)

// Lookup fetches the key for a, with the advanced method first and the
// direct method only if the openpgpkey subdomain does not exist (NXDOMAIN).
// It returns the raw response; the browser parses and judges the key.
func (c *Client) Lookup(ctx context.Context, a Address) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	key, err := c.get(ctx, a.AdvancedURL())
	if isNXDOMAIN(err) {
		key, err = c.get(ctx, a.DirectURL())
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, upstreamError{err}
	}
	return key, err
}

func (c *Client) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxKeySize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxKeySize {
		return nil, errTooLarge
	}
	if len(body) == 0 {
		return nil, ErrNotFound
	}
	return body, nil
}

func isNXDOMAIN(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// upstreamError hides the cause (which may contain the URL) behind
// ErrUpstream; Reason gives a coarse category that is safe to log.
type upstreamError struct{ cause error }

func (e upstreamError) Error() string { return ErrUpstream.Error() }
func (e upstreamError) Is(target error) bool {
	return target == ErrUpstream
}

// Reason is a fixed, non-identifying category for a lookup error, for
// logs and metrics. It never contains the address, domain or URL.
func Reason(err error) string {
	var u upstreamError
	if !errors.As(err, &u) {
		return "other"
	}
	var dnsErr *net.DNSError
	var tlsErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.Is(u.cause, errBlocked):
		return "blocked address"
	case errors.Is(u.cause, errRedirect):
		return "redirect"
	case errors.Is(u.cause, errTooLarge):
		return "too large"
	case errors.As(u.cause, &dnsErr):
		return "dns"
	case errors.As(u.cause, &tlsErr):
		return "tls"
	case errors.Is(u.cause, context.DeadlineExceeded), errors.As(u.cause, &netErr) && netErr.Timeout():
		return "timeout"
	default:
		return "upstream"
	}
}
