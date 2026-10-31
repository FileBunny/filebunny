// Package mailtest is a fake SMTP relay for tests: TLS with a throwaway
// certificate, STARTTLS or implicit TLS, AUTH PLAIN, and the received
// messages in memory. It never sends anything on.
package mailtest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/mail"
)

// Received is one delivered message.
type Received struct {
	From, To string
	Data     string
}

// Server is a fake relay. Set the Fail* fields before sending to make it
// answer with that SMTP code.
type Server struct {
	// Relay is the configuration that reaches this server.
	Relay mail.Relay
	// ClientTLS trusts the server's certificate; use it as mail.Sender.TLS.
	ClientTLS *tls.Config

	mu       sync.Mutex
	received []Received
	FailRcpt int
	FailMail int
	FailAuth bool

	ln       net.Listener
	tls      *tls.Config
	implicit bool
}

const (
	user     = "relay-user"
	password = "relay-secret"
)

// Start runs a fake relay until the test ends. With implicit, clients
// connect with TLS right away; otherwise they must use STARTTLS.
func Start(t testing.TB, name string, implicit bool) *Server {
	t.Helper()
	cert, pool := certificate(t)
	s := &Server{
		tls:       &tls.Config{Certificates: []tls.Certificate{cert}},
		ClientTLS: &tls.Config{RootCAs: pool},
		implicit:  implicit,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if implicit {
		ln = tls.NewListener(ln, s.tls)
	}
	s.ln = ln
	port := ln.Addr().(*net.TCPAddr).Port
	s.Relay = mail.Relay{Name: name, Host: "127.0.0.1", Port: port, Username: user, Password: password,
		DailyLimit: 100, ImplicitTLS: implicit}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

// Messages returns what was delivered so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.received...)
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.session(c)
	}
}

func (s *Server) session(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	say := func(line string) {
		w.WriteString(line + "\r\n")
		w.Flush()
	}
	secure := s.implicit
	var from, to string
	say("220 fake relay")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(cmd, " ", 2)[0])
		switch {
		case verb == "EHLO":
			if secure {
				say("250-fake\r\n250 AUTH PLAIN")
			} else {
				say("250-fake\r\n250 STARTTLS")
			}
		case verb == "STARTTLS" && !secure:
			say("220 go ahead")
			tc := tls.Server(c, s.tls)
			if tc.Handshake() != nil {
				return
			}
			c, secure = tc, true
			r, w = bufio.NewReader(c), bufio.NewWriter(c)
		case verb == "AUTH":
			want := base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + password))
			if !secure || s.FailAuth || !strings.HasSuffix(cmd, " "+want) {
				say("535 authentication failed")
				continue
			}
			say("235 ok")
		case verb == "MAIL":
			if !secure {
				say("530 must issue STARTTLS first")
				continue
			}
			if s.FailMail != 0 {
				say(strconv.Itoa(s.FailMail) + " refused")
				continue
			}
			from = between(cmd)
			say("250 ok")
		case verb == "RCPT":
			to = between(cmd)
			if s.FailRcpt != 0 {
				// Real relays often echo the address; it must not leak.
				say(strconv.Itoa(s.FailRcpt) + " " + to + " refused")
				continue
			}
			say("250 ok")
		case verb == "DATA":
			say("354 go on")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, "."))
			}
			s.mu.Lock()
			s.received = append(s.received, Received{From: from, To: to, Data: data.String()})
			s.mu.Unlock()
			say("250 queued")
		case verb == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func between(cmd string) string {
	_, rest, _ := strings.Cut(cmd, "<")
	addr, _, _ := strings.Cut(rest, ">")
	return addr
}

func certificate(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake relay"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}
