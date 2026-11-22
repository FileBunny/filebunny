// Package pow is a proof-of-work check: before an expensive request, the
// client gets a challenge and must find a number of counters (Proofs) so
// that SHA-256(challenge "." counter) starts with the required number of
// zero bits for each. This costs a browser a few seconds at most per
// request and makes mass use expensive, without captchas or tracking.
//
// The work is split into Proofs smaller puzzles instead of one: the
// average is the same, but the time a client needs varies much less (a
// single puzzle takes anywhere from a twentieth to three times the
// average), and progress can be shown honestly.
//
// Challenges are stateless (signed with a key that exists only in memory)
// and bound to an action and to the visitor (ratelimit.Client). A solved
// challenge works once: used ones are remembered until they expire.
package pow

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/bits"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lifetime is how long a challenge can be solved and used.
const Lifetime = 10 * time.Minute

// MaxBits caps the difficulty; 28 bits is about a minute in a browser.
const MaxBits = 28

// proofsLog2 sets the number of smaller puzzles: 1<<proofsLog2 = 16, each
// with that many bits fewer than the difficulty, so the total work stays
// 2^difficulty hashes on average.
const proofsLog2 = 4

// maxCounterLen caps one counter: up to 20 digits (uint64).
const maxCounterLen = 20

// maxUsed caps the used challenges remembered at once.
const maxUsed = 200_000

var (
	// ErrMissing means the request carried no proof.
	ErrMissing = errors.New("pow: no proof")
	// ErrInvalid means the proof is malformed, for another action or
	// visitor, expired, not enough work, or used before.
	ErrInvalid = errors.New("pow: invalid proof")
	// ErrBusy means too many proofs were used recently to remember another.
	ErrBusy = errors.New("pow: too many proofs in use")
)

// Challenge is what the client gets: find Proofs counters, each giving
// Bits leading zero bits.
type Challenge struct {
	Token   string
	Bits    int
	Proofs  int
	Expires time.Time
}

// Issuer is safe for concurrent use.
type Issuer struct {
	key [32]byte
	now func() time.Time
	max int

	mu   sync.Mutex
	used map[[16]byte]int64 // nonce → expiry (unix seconds)
}

// New returns an issuer with a random key; a restart invalidates all
// challenges.
func New(now func() time.Time) *Issuer {
	i := &Issuer{now: now, max: maxUsed, used: map[[16]byte]int64{}}
	rand.Read(i.key[:])
	return i
}

// token layout: nonce (16) | expiry (8) | bits (1) | proofs (1) | mac (32).
const (
	nonceLen   = 16
	bitsAt     = nonceLen + 8
	proofsAt   = bitsAt + 1
	payloadLen = proofsAt + 1
	tokenLen   = payloadLen + sha256.Size
)

func (i *Issuer) mac(payload []byte, action string, client [16]byte) []byte {
	h := hmac.New(sha256.New, i.key[:])
	h.Write(payload)
	h.Write([]byte{0})
	h.Write([]byte(action))
	h.Write([]byte{0})
	h.Write(client[:])
	return h.Sum(nil)
}

// Issue returns a challenge of the given difficulty (log2 of the average
// number of hashes) for action and client.
func (i *Issuer) Issue(action string, client [16]byte, difficulty int) Challenge {
	difficulty = max(0, min(difficulty, MaxBits))
	split := min(difficulty, proofsLog2)
	bits, proofs := difficulty-split, 1<<split
	exp := i.now().Add(Lifetime).Truncate(time.Second)
	b := make([]byte, payloadLen, tokenLen)
	rand.Read(b[:nonceLen])
	binary.BigEndian.PutUint64(b[nonceLen:], uint64(exp.Unix()))
	b[bitsAt] = byte(bits)
	b[proofsAt] = byte(proofs)
	b = append(b, i.mac(b, action, client)...)
	return Challenge{Token: base64.RawURLEncoding.EncodeToString(b), Bits: bits, Proofs: proofs, Expires: exp}
}

// maxProofLen caps a proof: the token and 1<<proofsLog2 counters.
var maxProofLen = base64.RawURLEncoding.EncodedLen(tokenLen) + (1<<proofsLog2)*(1+maxCounterLen)

// Verify checks a proof ("token.counter1.counter2…", the counters in
// ascending order) for action and client and marks it used.
func (i *Issuer) Verify(proof, action string, client [16]byte) error {
	if proof == "" {
		return ErrMissing
	}
	if len(proof) > maxProofLen {
		return ErrInvalid
	}
	token, rest, ok := strings.Cut(proof, ".")
	if !ok || len(token) != base64.RawURLEncoding.EncodedLen(tokenLen) {
		return ErrInvalid
	}
	counters := strings.Split(rest, ".")
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != tokenLen {
		return ErrInvalid
	}
	payload := b[:payloadLen]
	if !hmac.Equal(b[payloadLen:], i.mac(payload, action, client)) {
		return ErrInvalid
	}
	exp := int64(binary.BigEndian.Uint64(payload[nonceLen:]))
	now := i.now().Unix()
	if now >= exp {
		return ErrInvalid
	}
	if len(counters) != int(payload[proofsAt]) {
		return ErrInvalid
	}
	bits := int(payload[bitsAt])
	var prev uint64
	for k, c := range counters {
		n, err := strconv.ParseUint(c, 10, 64)
		// Ascending, so one counter cannot count twice; canonical, so
		// "07" is not another spelling of "7".
		if err != nil || len(c) > maxCounterLen || c != strconv.FormatUint(n, 10) || (k > 0 && n <= prev) {
			return ErrInvalid
		}
		if LeadingZeroBits(sha256.Sum256([]byte(token+"."+c))) < bits {
			return ErrInvalid
		}
		prev = n
	}

	var nonce [16]byte
	copy(nonce[:], payload[:nonceLen])
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, seen := i.used[nonce]; seen {
		return ErrInvalid
	}
	if len(i.used) >= i.max {
		for n, e := range i.used {
			if e <= now {
				delete(i.used, n)
			}
		}
		if len(i.used) >= i.max {
			return ErrBusy
		}
	}
	i.used[nonce] = exp
	return nil
}

// LeadingZeroBits counts the zero bits at the start of sum.
func LeadingZeroBits(sum [32]byte) int {
	n := 0
	for _, c := range sum {
		if c != 0 {
			return n + bits.LeadingZeros8(c)
		}
		n += 8
	}
	return n
}

// Solve finds a proof by brute force; for tests and tools, not the server.
func Solve(c Challenge) string {
	proof := c.Token
	for n, found := uint64(0), 0; found < max(c.Proofs, 1); n++ {
		counter := strconv.FormatUint(n, 10)
		if LeadingZeroBits(sha256.Sum256([]byte(c.Token+"."+counter))) >= c.Bits {
			proof += "." + counter
			found++
		}
	}
	return proof
}
