// Package wkd fetches OpenPGP keys from a Web Key Directory
// (draft-koch-openpgp-webkey-service) on behalf of the browser.
//
// The domain comes from an anonymous client, so every request is an SSRF
// risk: see client.go for the measures. Nothing in this package logs, and
// its errors never contain the address, domain, hash or URL.
package wkd

import (
	"crypto/sha1"
	"errors"
	"net/url"
	"strings"
)

// Limits from RFC 5321 (path, local part) and RFC 1035 (names, labels).
const (
	maxAddressLen = 254
	maxLocalLen   = 64
	maxDomainLen  = 253
	maxLabelLen   = 63
)

// ErrInvalidAddress means the e-mail address is not one we look up.
var ErrInvalidAddress = errors.New("invalid e-mail address")

// Address is a validated e-mail address.
type Address struct {
	// Local is the local part as given; WKD sends it unchanged in "?l=".
	Local string
	// Domain is lowercased ASCII.
	Domain string
}

// ParseAddress validates an e-mail address strictly: exactly one "@", a
// dot-atom local part, and a domain of at least two ASCII
// letter-digit-hyphen labels. Internationalized domains must be given in
// punycode ("xn--..."); converting them would need an IDNA library. IP
// literals, ports and trailing dots are refused, so the domain can only
// ever be a DNS name on the public internet.
func ParseAddress(s string) (Address, error) {
	if len(s) > maxAddressLen || strings.Count(s, "@") != 1 {
		return Address{}, ErrInvalidAddress
	}
	local, domain, _ := strings.Cut(s, "@")
	if !validLocal(local) || !validDomain(domain) {
		return Address{}, ErrInvalidAddress
	}
	return Address{Local: local, Domain: strings.ToLower(domain)}, nil
}

// validLocal accepts the RFC 5322 dot-atom: atext characters, with single
// dots that neither start nor end it. No quoted strings.
func validLocal(s string) bool {
	if s == "" || len(s) > maxLocalLen || s[0] == '.' || s[len(s)-1] == '.' || strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isAlnum(c) && !strings.ContainsRune("!#$%&'*+-/=?^_`{|}~.", rune(c)) {
			return false
		}
	}
	return true
}

func validDomain(s string) bool {
	if len(s) > maxDomainLen {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxLabelLen || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			if !isAlnum(l[i]) && l[i] != '-' {
				return false
			}
		}
	}
	// A numeric top-level label would make "10.0.0.1" a domain.
	tld := labels[len(labels)-1]
	return strings.IndexFunc(tld, func(r rune) bool { return r < '0' || r > '9' }) >= 0
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// Hash is the WKD hash of the local part: SHA-1 of the lowercased local
// part, z-base32 encoded.
func (a Address) Hash() string {
	sum := sha1.Sum([]byte(strings.ToLower(a.Local)))
	return zbase32(sum[:])
}

// AdvancedURL is the URL of the advanced method, on the openpgpkey
// subdomain.
func (a Address) AdvancedURL() string {
	return "https://openpgpkey." + a.Domain + "/.well-known/openpgpkey/" + a.Domain + "/hu/" + a.Hash() + "?l=" + url.QueryEscape(a.Local)
}

// DirectURL is the URL of the direct method, on the domain itself.
func (a Address) DirectURL() string {
	return "https://" + a.Domain + "/.well-known/openpgpkey/hu/" + a.Hash() + "?l=" + url.QueryEscape(a.Local)
}

const zbase32Alphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

// zbase32 encodes b in z-base-32 (5 bits per character, no padding).
func zbase32(b []byte) string {
	var out strings.Builder
	var buf, bits uint
	for _, c := range b {
		buf = buf<<8 | uint(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(zbase32Alphabet[buf>>bits&31])
		}
	}
	if bits > 0 {
		out.WriteByte(zbase32Alphabet[buf<<(5-bits)&31])
	}
	return out.String()
}
