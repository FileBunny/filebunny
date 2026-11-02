package server

import (
	"context"
	"errors"
	"math/bits"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/pow"
	"github.com/FileBunny/filebunny/backend/internal/ratelimit"
)

// Limits per visitor (a daily-salted hash of the address, see
// internal/ratelimit). Generous for people, a wall for scripts; proof of
// work makes each step up more expensive.
var visitorLimits = map[ratelimit.Action]ratelimit.Limit{
	ratelimit.Upload:      {PerHour: 60, PerDay: 300},
	ratelimit.UploadBytes: {PerHour: 1 << 30, PerDay: 4 << 30},
	ratelimit.Inbox:       {PerHour: 10, PerDay: 30},
	ratelimit.Mail:        {PerHour: 10, PerDay: 30},
	ratelimit.WKD:         {PerHour: 60, PerDay: 300},
	ratelimit.Download:    {PerHour: 600, PerDay: 3000},
}

// powHeader carries the proof of work ("challenge.counter").
const powHeader = "FileBunny-PoW"

// powActions are the requests that need proof of work, and the counter
// that raises their difficulty.
var powActions = map[string]ratelimit.Action{
	"upload": ratelimit.Upload,
	"inbox":  ratelimit.Inbox,
	"mail":   ratelimit.Mail,
}

type clientKey struct{}

// identifyClient hashes the visitor's address into the request context and
// removes the header it came in, so nothing after this sees the address.
func (s *Server) identifyClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var addr netip.Addr
		if h := s.cfg.ClientIPHeader; h != "" {
			addr, _ = netip.ParseAddr(r.Header.Get(h))
			r.Header.Del(h)
		}
		if !addr.IsValid() {
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				addr, _ = netip.ParseAddr(host)
			}
		}
		// An unknown address (only possible without the proxy) shares
		// the zero address's bucket.
		if !addr.IsValid() {
			addr = netip.IPv4Unspecified()
		}
		c := s.limiter.Client(addr)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientKey{}, c)))
	})
}

func client(r *http.Request) ratelimit.Client {
	c, _ := r.Context().Value(clientKey{}).(ratelimit.Client)
	return c
}

// allow counts n of action a for the visitor. Over a limit it answers 429
// (or 503 when too many visitors are tracked) and returns false.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, a ratelimit.Action, n int64) bool {
	if !s.cfg.RateLimits {
		return true
	}
	var err error
	if n == 0 {
		err = s.limiter.Room(client(r), a)
	} else {
		err = s.limiter.Allow(client(r), a, n)
	}
	return s.limitError(w, err)
}

func (s *Server) limitError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ratelimit.ErrLimited):
		w.Header().Set("Retry-After", strconv.Itoa(int(s.limiter.RetryAfter()/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "you reached a limit of this server, try again later")
	default:
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable, "this server is busy, try again in a minute")
	}
	return false
}

// counted adds what an upload turned out to be, after it was stored.
func (s *Server) counted(r *http.Request, a ratelimit.Action, n int64) {
	s.limiter.Add(client(r), a, n)
}

// proven checks the proof of work for action. Without a valid one it
// answers 403 and returns false.
func (s *Server) proven(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.pow == nil {
		return true
	}
	err := s.pow.Verify(r.Header.Get(powHeader), action, client(r))
	switch {
	case err == nil:
		return true
	case errors.Is(err, pow.ErrBusy):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable, "this server is busy, try again in a minute")
	default:
		writeError(w, http.StatusForbidden, "proof of work missing or not valid; get a challenge from GET /api/v1/challenge?action="+action)
	}
	return false
}

// difficulty is the base difficulty plus one bit per doubling of what the
// visitor did this hour (from 4 on).
func (s *Server) difficulty(c ratelimit.Client, a ratelimit.Action) int {
	return s.cfg.PowBits + bits.Len64(uint64(s.limiter.Count(c, a)/4))
}

type challengeResponse struct {
	Challenge string    `json:"challenge"`
	Bits      int       `json:"bits"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// handleChallenge issues a proof-of-work challenge for ?action=upload,
// inbox or mail. With proof of work off it answers {"challenge": "",
// "bits": 0} and requests need no proof.
func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	counter, ok := powActions[action]
	if !ok {
		writeError(w, http.StatusBadRequest, "action must be upload, inbox or mail")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.pow == nil {
		writeJSON(w, http.StatusOK, challengeResponse{})
		return
	}
	c := s.pow.Issue(action, client(r), s.difficulty(client(r), counter))
	writeJSON(w, http.StatusOK, challengeResponse{Challenge: c.Token, Bits: c.Bits, ExpiresAt: c.Expires.UTC()})
}
