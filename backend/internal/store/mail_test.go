package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMailPepper(t *testing.T) {
	s, dir := openTest(t)
	a, err := s.MailPepper()
	if err != nil || len(a) != 32 {
		t.Fatalf("%x, %v", a, err)
	}
	b, _ := s.MailPepper()
	if !bytes.Equal(a, b) {
		t.Error("pepper changed")
	}
	info, err := os.Stat(filepath.Join(dir, pepperFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("pepper file: %v, %v", info, err)
	}
}

func TestShareLinkNeedsDeleteToken(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, token, err := s.Put(ctx, bytes.NewReader([]byte("x")), t0, t0.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ShareLink(ctx, f.ID, token, t0); err != nil || got.ID != f.ID || got.Downloads != 0 {
		t.Errorf("right token: %+v, %v", got, err)
	}
	if _, err := s.ShareLink(ctx, f.ID, "wrong", t0); err != ErrForbidden {
		t.Errorf("wrong token: %v", err)
	}
	if _, err := s.ShareLink(ctx, f.ID, token, t0.Add(2*time.Hour)); err != ErrNotFound {
		t.Errorf("expired: %v", err)
	}
}

func TestClaimMail(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	put := func() string {
		f, _, err := s.Put(ctx, bytes.NewReader([]byte("x")), t0, t0.Add(48*time.Hour), 0)
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	lim := MailLimits{PerFile: 2, PerRecipient: 2, Daily: 3}
	a, b := []byte("hash-a"), []byte("hash-b")
	f1, f2 := put(), put()

	if err := s.ClaimMail(ctx, f1, a, lim, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimMail(ctx, f1, b, lim, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimMail(ctx, f1, a, lim, t0); err != ErrMailFileLimit {
		t.Errorf("third mail of one link: %v", err)
	}
	if err := s.ClaimMail(ctx, f2, a, lim, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimMail(ctx, f2, a, lim, t0); err != ErrMailRecipientLimit {
		t.Errorf("third mail to one address: %v", err)
	}
	if err := s.ClaimMail(ctx, put(), []byte("hash-c"), lim, t0); err != ErrMailDailyLimit {
		t.Errorf("fourth mail of the day: %v", err)
	}
	// A refused claim counts nothing: f2 still has one mail left.
	next := t0.Add(24 * time.Hour)
	if err := s.ClaimMail(ctx, f2, b, lim, next); err != nil {
		t.Errorf("next day: %v", err)
	}
	if _, err := s.DeleteExpired(ctx, next); err != nil {
		t.Fatal(err)
	}
	var old int
	s.db.QueryRow(`SELECT COUNT(*) FROM mail_counts WHERE day < ?`, mailDay(next)).Scan(&old)
	if old != 0 {
		t.Errorf("%d counters of past days left", old)
	}
}

func TestClaimRelay(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for i, want := range []bool{true, true, false} {
		if ok, err := s.ClaimRelay(ctx, "one", 2, t0); err != nil || ok != want {
			t.Errorf("claim %d: %v, %v", i+1, ok, err)
		}
	}
	if ok, _ := s.ClaimRelay(ctx, "two", 2, t0); !ok {
		t.Error("other relay has its own count")
	}
	if ok, _ := s.ClaimRelay(ctx, "one", 2, t0.Add(24*time.Hour)); !ok {
		t.Error("count did not reset the next day")
	}
}

func TestMailBlocks(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	h := []byte("hash")
	if blocked, _ := s.MailBlocked(ctx, h); blocked {
		t.Fatal("blocked from the start")
	}
	for range 2 {
		if err := s.BlockMail(ctx, h, t0); err != nil {
			t.Fatal(err)
		}
	}
	if blocked, _ := s.MailBlocked(ctx, h); !blocked {
		t.Error("not blocked")
	}
	if err := s.UnblockMail(ctx, h); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := s.MailBlocked(ctx, h); blocked {
		t.Error("still blocked")
	}
}
