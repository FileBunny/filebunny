package storage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http/httptest"
	"net/netip"
)

// testClient returns options that send requests for hosts to srv, a TLS
// test server on loopback.
func testClient(srv *httptest.Server, hosts ...string) clientOptions {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	o := clientOptions{
		hosts: make(map[string]bool, len(hosts)),
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		port:    port,
		allowed: func(netip.Addr) bool { return true },
		// The test certificate is for example.com; check against that name.
		tls: &tls.Config{RootCAs: pool, ServerName: "example.com"},
	}
	for _, h := range hosts {
		o.hosts[h] = true
	}
	return o
}
