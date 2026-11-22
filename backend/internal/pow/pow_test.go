package pow

import (
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func newTest() (*Issuer, *time.Time) {
	now := t0
	return New(func() time.Time { return now }), &now
}

var alice, bob = [16]byte{1}, [16]byte{2}

func TestSolveAndVerify(t *testing.T) {
	i, _ := newTest()
	c := i.Issue("upload", alice, 12)
	if c.Bits != 8 || c.Proofs != 16 || !c.Expires.Equal(t0.Add(Lifetime)) {
		t.Fatalf("challenge %+v", c)
	}
	proof := Solve(c)
	token, rest, _ := strings.Cut(proof, ".")
	counters := strings.Split(rest, ".")
	if token != c.Token || len(counters) != 16 {
		t.Fatalf("proof %q", proof)
	}
	for _, n := range counters {
		if LeadingZeroBits(sha256.Sum256([]byte(token+"."+n))) < 8 {
			t.Fatal("solver returned too little work")
		}
	}
	if err := i.Verify(proof, "upload", alice); err != nil {
		t.Fatalf("valid proof: %v", err)
	}
	if err := i.Verify(proof, "upload", alice); err != ErrInvalid {
		t.Errorf("proof used twice: %v", err)
	}
}

func TestVerifyRefuses(t *testing.T) {
	i, now := newTest()
	c := i.Issue("upload", alice, 10)
	proof := Solve(c)
	token, rest, _ := strings.Cut(proof, ".")
	counters := strings.Split(rest, ".")
	with := func(cs []string) string { return token + "." + strings.Join(cs, ".") }
	repeated := slices.Clone(counters)
	repeated[1] = repeated[0]
	swapped := slices.Clone(counters)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	padded := slices.Clone(counters)
	padded[3] = "0" + padded[3]
	cases := map[string]struct {
		proof, action string
		client        [16]byte
	}{
		"other action":      {proof, "mail", alice},
		"other visitor":     {proof, "upload", bob},
		"no counter":        {token, "upload", alice},
		"bad counter":       {token + ".x", "upload", alice},
		"garbage":           {"abc.1", "upload", alice},
		"one counter (old)": {token + "." + counters[0], "upload", alice},
		"too few":           {with(counters[:15]), "upload", alice},
		"too many":          {proof + ".99999999", "upload", alice},
		"repeated":          {with(repeated), "upload", alice},
		"not ascending":     {with(swapped), "upload", alice},
		"leading zero":      {with(padded), "upload", alice},
		"empty counter":     {proof + ".", "upload", alice},
		"too long":          {proof + strings.Repeat("1", maxProofLen), "upload", alice},
	}
	for name, tc := range cases {
		if err := i.Verify(tc.proof, tc.action, tc.client); err != ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := i.Verify("", "upload", alice); err != ErrMissing {
		t.Errorf("no proof: %v", err)
	}

	// Not enough work: the last counter replaced by one whose hash misses
	// the target (and is still the largest).
	last, _ := strconv.ParseUint(counters[15], 10, 64)
	for n := last + 1; ; n++ {
		if LeadingZeroBits(sha256.Sum256([]byte(token+"."+strconv.FormatUint(n, 10)))) < c.Bits {
			weak := slices.Clone(counters)
			weak[15] = strconv.FormatUint(n, 10)
			if err := i.Verify(with(weak), "upload", alice); err != ErrInvalid {
				t.Errorf("too little work: %v", err)
			}
			break
		}
	}
	// Lowering the difficulty or the number of proofs in the token breaks
	// the signature.
	for _, at := range []int{bitsAt, proofsAt} {
		b, _ := base64.RawURLEncoding.DecodeString(i.Issue("upload", alice, 20).Token)
		b[at] = 1
		if err := i.Verify(base64.RawURLEncoding.EncodeToString(b)+".0", "upload", alice); err != ErrInvalid {
			t.Errorf("tampered byte %d: %v", at, err)
		}
	}
	// So does a challenge from another key.
	other := New(func() time.Time { return t0 }).Issue("upload", alice, 0)
	if err := i.Verify(Solve(other), "upload", alice); err != ErrInvalid {
		t.Errorf("challenge from another key: %v", err)
	}

	*now = now.Add(Lifetime)
	if err := i.Verify(proof, "upload", alice); err != ErrInvalid {
		t.Errorf("expired: %v", err)
	}
}

func TestUsedIsCapped(t *testing.T) {
	i, now := newTest()
	i.max = 1
	if err := i.Verify(Solve(i.Issue("upload", alice, 0)), "upload", alice); err != nil {
		t.Fatal(err)
	}
	if err := i.Verify(Solve(i.Issue("upload", alice, 0)), "upload", alice); err != ErrBusy {
		t.Errorf("over the cap: %v", err)
	}
	// Expired entries make room.
	*now = now.Add(Lifetime)
	if err := i.Verify(Solve(i.Issue("upload", alice, 0)), "upload", alice); err != nil {
		t.Errorf("after expiry: %v", err)
	}
}

func TestLeadingZeroBits(t *testing.T) {
	cases := map[[32]byte]int{{}: 256, {0x80}: 0, {0x01}: 7, {0, 0x10}: 11}
	for sum, want := range cases {
		if got := LeadingZeroBits(sum); got != want {
			t.Errorf("%x: %d, want %d", sum[:2], got, want)
		}
	}
}

func TestDifficultyCapped(t *testing.T) {
	i, _ := newTest()
	if c := i.Issue("upload", alice, 99); c.Bits != MaxBits-4 || c.Proofs != 16 {
		t.Errorf("bits %d, proofs %d", c.Bits, c.Proofs)
	}
}

// Below 4 bits there are fewer proofs, so the average stays 2^difficulty.
func TestLowDifficulty(t *testing.T) {
	i, _ := newTest()
	for d, want := range map[int][2]int{0: {0, 1}, 1: {0, 2}, 3: {0, 8}, 4: {0, 16}, 5: {1, 16}, 16: {12, 16}} {
		c := i.Issue("upload", alice, d)
		if c.Bits != want[0] || c.Proofs != want[1] {
			t.Errorf("difficulty %d: bits %d, proofs %d, want %v", d, c.Bits, c.Proofs, want)
		}
		if err := i.Verify(Solve(c), "upload", alice); err != nil {
			t.Errorf("difficulty %d: %v", d, err)
		}
	}
}
