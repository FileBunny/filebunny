// Package ratelimit counts what each visitor does, without knowing who
// the visitor is.
//
// A visitor is a keyed hash of their IP address (IPv6 by /64), with a
// random salt that exists only in memory and changes every UTC day. The
// address itself is never stored; the hash cannot be linked to the
// previous day's, and nothing survives a restart. Counters live in memory
// only, for at most maxClients visitors.
package ratelimit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// Client identifies a visitor for one day. It is not an address and
// cannot be turned back into one.
type Client [16]byte

// Action is something that is counted.
type Action int

const (
	Upload      Action = iota // single upload, or a chunked upload started
	UploadBytes               // bytes received by uploads
	Inbox                     // upload link created
	Mail                      // share link mailed
	WKD                       // WKD lookup
	Download                  // download started
	numActions
)

// Limit is the most of an action per clock hour and per UTC day; 0 means
// no limit for that period.
type Limit struct {
	PerHour, PerDay int64
}

var (
	// ErrLimited means the visitor reached a limit.
	ErrLimited = errors.New("ratelimit: limit reached")
	// ErrBusy means too many visitors are tracked to add another.
	ErrBusy = errors.New("ratelimit: too many visitors")
)

// maxClients caps the visitors tracked at once (a few hundred bytes each).
const maxClients = 200_000

type usage struct {
	hour   int64
	hourly [numActions]int64
	daily  [numActions]int64
}

// Limiter is safe for concurrent use.
type Limiter struct {
	limits [numActions]Limit
	now    func() time.Time
	max    int

	mu      sync.Mutex
	day     int64
	salt    [32]byte
	clients map[Client]*usage
}

// New returns a limiter with the given limits; actions without an entry
// are not limited (but still counted, for Count).
func New(limits map[Action]Limit, now func() time.Time) *Limiter {
	l := &Limiter{now: now, max: maxClients, clients: map[Client]*usage{}, day: -1}
	for a, lim := range limits {
		l.limits[a] = lim
	}
	return l
}

// rotate starts a new day: a new salt, so today's hashes cannot be linked
// to yesterday's, and no counters. The caller holds mu.
func (l *Limiter) rotate(now time.Time) {
	day := now.Unix() / 86400
	if day == l.day {
		return
	}
	l.day = day
	rand.Read(l.salt[:])
	clear(l.clients)
}

// Client hashes an address. IPv4 (also mapped into IPv6) counts per
// address, IPv6 per /64, which is what one connection usually gets.
func (l *Limiter) Client(addr netip.Addr) Client {
	addr = addr.Unmap()
	if addr.Is6() {
		addr = netip.PrefixFrom(addr, 64).Masked().Addr()
	}
	b := addr.AsSlice()
	l.mu.Lock()
	l.rotate(l.now())
	h := hmac.New(sha256.New, l.salt[:])
	l.mu.Unlock()
	h.Write(b)
	var c Client
	copy(c[:], h.Sum(nil))
	return c
}

// usage returns c's counters for the current hour, adding c if needed.
// The caller holds mu.
func (l *Limiter) usage(c Client, now time.Time) (*usage, error) {
	l.rotate(now)
	hour := now.Unix() / 3600
	u := l.clients[c]
	if u == nil {
		if len(l.clients) >= l.max {
			l.prune(hour)
			if len(l.clients) >= l.max {
				return nil, ErrBusy
			}
		}
		u = &usage{hour: hour}
		l.clients[c] = u
	}
	if u.hour != hour {
		u.hour, u.hourly = hour, [numActions]int64{}
	}
	return u, nil
}

// prune drops visitors that did nothing this hour; their daily counts go
// with them, which only errs on the lenient side when the table is full.
func (l *Limiter) prune(hour int64) {
	for c, u := range l.clients {
		if u.hour != hour {
			delete(l.clients, c)
		}
	}
}

// Allow counts n of action a for c if that stays within the limits.
func (l *Limiter) Allow(c Client, a Action, n int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.allow(c, a, n)
}

// Reserve counts n of action a for c if that stays within the limits, like
// Allow, for amounts that are only known afterwards, such as the bytes of
// an upload: reserve the most it can be, then settle with what was used.
// Concurrent requests cannot all pass a check for the same room this way.
// settle must be called once; used above n counts only n. A reservation
// made in an earlier hour no longer changes the hourly count (that hour's
// count is gone), one from an earlier day changes nothing.
func (l *Limiter) Reserve(c Client, a Action, n int64) (settle func(used int64), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.allow(c, a, n); err != nil {
		return nil, err
	}
	now := l.now()
	day, hour := l.day, now.Unix()/3600
	var once sync.Once
	return func(used int64) {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.rotate(l.now())
			u := l.clients[c]
			if l.day != day || u == nil {
				return
			}
			back := n - min(max(used, 0), n)
			if u.hour == hour {
				u.hourly[a] = max(u.hourly[a]-back, 0)
			}
			u.daily[a] = max(u.daily[a]-back, 0)
		})
	}, nil
}

// errNegative guards against a caller counting backwards, which would
// give a visitor room they did not have.
var errNegative = errors.New("ratelimit: negative count")

// allow is Allow with mu held.
func (l *Limiter) allow(c Client, a Action, n int64) error {
	if n < 0 {
		return errNegative
	}
	u, err := l.usage(c, l.now())
	if err != nil {
		return err
	}
	lim := l.limits[a]
	if (lim.PerHour > 0 && u.hourly[a]+n > lim.PerHour) || (lim.PerDay > 0 && u.daily[a]+n > lim.PerDay) {
		return ErrLimited
	}
	u.hourly[a] += n
	u.daily[a] += n
	return nil
}

// Room reports whether c has any of action a left, without counting. To
// count an amount only known afterwards, use Reserve.
func (l *Limiter) Room(c Client, a Action) error {
	return l.Allow(c, a, 0)
}

// Add counts n of action a for c without checking the limits. A negative
// n is ignored.
func (l *Limiter) Add(c Client, a Action, n int64) {
	if n < 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if u, err := l.usage(c, l.now()); err == nil {
		u.hourly[a] += n
		u.daily[a] += n
	}
}

// Count returns how many of action a c did this hour.
func (l *Limiter) Count(c Client, a Action) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rotate(l.now())
	u := l.clients[c]
	if u == nil || u.hour != l.now().Unix()/3600 {
		return 0
	}
	return u.hourly[a]
}

// RetryAfter is the time until the current hour ends, for Retry-After.
func (l *Limiter) RetryAfter() time.Duration {
	now := l.now()
	return now.Truncate(time.Hour).Add(time.Hour).Sub(now)
}
