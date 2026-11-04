package pow

import (
	"crypto/sha256"
	"encoding/base64"
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
	if c.Bits != 12 || !c.Expires.Equal(t0.Add(Lifetime)) {
		t.Fatalf("challenge %+v", c)
	}
	proof := Solve(c)
	if LeadingZeroBits(sha256.Sum256([]byte(proof))) < 12 {
		t.Fatal("solver returned too little work")
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
	token, _, _ := strings.Cut(proof, ".")
	cases := map[string]struct {
		proof, action string
		client        [16]byte
	}{
		"other action":  {proof, "mail", alice},
		"other visitor": {proof, "upload", bob},
		"no counter":    {token, "upload", alice},
		"bad counter":   {token + ".x", "upload", alice},
		"garbage":       {"abc.1", "upload", alice},
	}
	for name, tc := range cases {
		if err := i.Verify(tc.proof, tc.action, tc.client); err != ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := i.Verify("", "upload", alice); err != ErrMissing {
		t.Errorf("no proof: %v", err)
	}

	// Not enough work: a counter whose hash misses the target.
	for n := 0; ; n++ {
		p := token + "." + strconv.Itoa(n)
		if LeadingZeroBits(sha256.Sum256([]byte(p))) < 10 {
			if err := i.Verify(p, "upload", alice); err != ErrInvalid {
				t.Errorf("too little work: %v", err)
			}
			break
		}
	}
	// Lowering the difficulty in the token breaks the signature.
	b, _ := base64.RawURLEncoding.DecodeString(i.Issue("upload", alice, 20).Token)
	b[payloadLen-1] = 0
	if err := i.Verify(base64.RawURLEncoding.EncodeToString(b)+".0", "upload", alice); err != ErrInvalid {
		t.Errorf("tampered difficulty: %v", err)
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
	if c := i.Issue("upload", alice, 99); c.Bits != MaxBits {
		t.Errorf("bits %d", c.Bits)
	}
}
