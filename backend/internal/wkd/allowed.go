package wkd

import "net/netip"

// blocked lists the ranges a lookup must never connect to, on top of what
// Allowed rejects with netip's own checks. IPv6 ranges that embed or
// tunnel to IPv4 are blocked whole, since they could reach a private IPv4
// address behind a translator.
var blocked = mustPrefixes(
	"0.0.0.0/8",          // "this network"
	"100.64.0.0/10",      // CGNAT
	"192.0.0.0/24",       // IETF protocol assignments
	"192.0.2.0/24",       // documentation (TEST-NET-1)
	"192.88.99.0/24",     // 6to4 relay anycast
	"198.18.0.0/15",      // benchmarking
	"198.51.100.0/24",    // documentation (TEST-NET-2)
	"203.0.113.0/24",     // documentation (TEST-NET-3)
	"240.0.0.0/4",        // reserved, includes broadcast
	"::/96",              // IPv4-compatible (deprecated)
	"::ffff:0:0/96",      // IPv4-mapped
	"64:ff9b::/96",       // NAT64
	"64:ff9b:1::/48",     // local-use NAT64
	"100::/64",           // discard
	"2001::/32",          // Teredo
	"2001:2::/48",        // benchmarking
	"2001:db8::/32",      // documentation
	"2002::/16",          // 6to4
	"3fff::/20",          // documentation
	"fec0::/10",          // site-local (deprecated)
	"169.254.169.254/32", // cloud metadata (also link-local; listed to be explicit)
)

// Allowed reports whether a lookup may connect to addr: only global
// unicast addresses outside every special-purpose range. Loopback,
// private (RFC 1918, fc00::/7), link-local, multicast and unspecified
// addresses fail netip's checks; the rest is in blocked.
func Allowed(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, v := range s {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}
