package storage

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/wkd"
)

// Timeouts for one request. The transfer itself is bounded by the
// caller's context, since 100 MiB can take minutes.
const (
	dialTimeout   = 10 * time.Second
	tlsTimeout    = 10 * time.Second
	headerTimeout = 30 * time.Second
	// maxReplySize caps the JSON or HTML the service answers with.
	maxReplySize = 64 << 10
)

var (
	errBlocked  = errors.New("host or address not allowed")
	errRedirect = errors.New("redirect refused")
)

// clientOptions are seams for tests; production uses publicClient.
type clientOptions struct {
	// hosts are the only host names a request may go to.
	hosts map[string]bool
	// lookup resolves a host name.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// port is dialed instead of the URL's port (always 443 in production).
	port string
	// allowed decides which addresses may be connected to.
	allowed func(netip.Addr) bool
	tls     *tls.Config
}

// publicClient returns the production options for a service on hosts:
// port 443, public addresses only (as for WKD).
func publicClient(hosts ...string) clientOptions {
	o := clientOptions{
		hosts: make(map[string]bool, len(hosts)),
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			for i := range ips {
				ips[i] = ips[i].Unmap()
			}
			return ips, err
		},
		port:    "443",
		allowed: wkd.Allowed,
	}
	for _, h := range hosts {
		o.hosts[h] = true
	}
	return o
}

// newClient returns an HTTP client that only connects to o.hosts, on
// o.port, at addresses o.allowed accepts (checked again on the dialed
// address, so DNS rebinding cannot reach other ones), and follows at most
// one redirect, over https to one of the same hosts. jar may be nil.
func newClient(o clientOptions, jar http.CookieJar) *http.Client {
	control := func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !o.allowed(ap.Addr()) || strconv.Itoa(int(ap.Port())) != o.port {
			return errBlocked
		}
		return nil
	}
	dialer := &net.Dialer{Timeout: dialTimeout, Control: control}
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
			if !o.hosts[host] {
				return nil, errBlocked
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
		ForceAttemptHTTP2:      true,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        time.Minute,
	}
	return &http.Client{
		Transport: transport,
		Jar:       jar,
		// One redirect, only over https to one of the fixed hosts.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 1 || req.URL.Scheme != "https" || !o.hosts[req.URL.Hostname()] {
				return errRedirect
			}
			return nil
		},
	}
}

func isHTML(contentType string) bool {
	return len(contentType) >= 9 && contentType[:9] == "text/html"
}

// unavailable drops the URL from a client error: it contains the blob's
// reference, which must never be logged.
func unavailable(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func drain(body io.ReadCloser) {
	io.Copy(io.Discard, io.LimitReader(body, maxReplySize))
	body.Close()
}
