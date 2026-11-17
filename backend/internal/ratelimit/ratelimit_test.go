package ratelimit

import (
	"net/netip"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC)

func newTest(limits map[Action]Limit) (*Limiter, *time.Time) {
	now := t0
	return New(limits, func() time.Time { return now }), &now
}

func TestClientHashing(t *testing.T) {
	l, now := newTest(nil)
	a := l.Client(netip.MustParseAddr("203.0.113.7"))
	if a != l.Client(netip.MustParseAddr("::ffff:203.0.113.7")) {
		t.Error("IPv4-mapped address is another client")
	}
	if a == l.Client(netip.MustParseAddr("203.0.113.8")) {
		t.Error("two IPv4 addresses are one client")
	}
	v6 := l.Client(netip.MustParseAddr("2001:db8:1:2::1"))
	if v6 != l.Client(netip.MustParseAddr("2001:db8:1:2:ffff::9")) {
		t.Error("one /64 is several clients")
	}
	if v6 == l.Client(netip.MustParseAddr("2001:db8:1:3::1")) {
		t.Error("two /64s are one client")
	}
	// A new day, a new salt: yesterday's hash means nothing.
	*now = now.Add(24 * time.Hour)
	if a == l.Client(netip.MustParseAddr("203.0.113.7")) {
		t.Error("hash did not change with the day")
	}
}

func TestAllow(t *testing.T) {
	l, now := newTest(map[Action]Limit{Upload: {PerHour: 2, PerDay: 3}, UploadBytes: {PerDay: 100}})
	c := l.Client(netip.MustParseAddr("203.0.113.7"))
	other := l.Client(netip.MustParseAddr("203.0.113.8"))
	for i, want := range []error{nil, nil, ErrLimited} {
		if err := l.Allow(c, Upload, 1); err != want {
			t.Errorf("upload %d: %v, want %v", i+1, err, want)
		}
	}
	if l.Allow(other, Upload, 1) != nil {
		t.Error("another client is limited too")
	}
	if l.Count(c, Upload) != 2 {
		t.Errorf("count %d", l.Count(c, Upload))
	}
	*now = now.Add(time.Hour)
	if l.Allow(c, Upload, 1) != nil {
		t.Error("next hour still limited")
	}
	if l.Allow(c, Upload, 1) != ErrLimited {
		t.Error("daily limit not applied")
	}
	if l.Allow(c, Mail, 1000) != nil {
		t.Error("an action without a limit was limited")
	}

	// Bytes: checked before, counted after.
	if l.Room(c, UploadBytes) != nil {
		t.Fatal("no room at the start")
	}
	l.Add(c, UploadBytes, 150)
	if l.Room(c, UploadBytes) != ErrLimited {
		t.Error("room after going over")
	}
}

func TestMaxClients(t *testing.T) {
	l, now := newTest(map[Action]Limit{Upload: {PerHour: 10}})
	l.max = 2
	a, b, c := l.Client(netip.MustParseAddr("192.0.2.1")), l.Client(netip.MustParseAddr("192.0.2.2")), l.Client(netip.MustParseAddr("192.0.2.3"))
	l.Allow(a, Upload, 1)
	l.Allow(b, Upload, 1)
	if l.Allow(c, Upload, 1) != ErrBusy {
		t.Error("table over its maximum")
	}
	// Visitors idle this hour make room.
	*now = now.Add(time.Hour)
	if l.Allow(c, Upload, 1) != nil {
		t.Error("idle visitors not pruned")
	}
}

func TestRetryAfter(t *testing.T) {
	l, _ := newTest(nil)
	if got := l.RetryAfter(); got != 30*time.Minute {
		t.Errorf("retry after %s", got)
	}
}

func TestReserve(t *testing.T) {
	l, now := newTest(map[Action]Limit{UploadBytes: {PerHour: 100, PerDay: 150}})
	c := l.Client(netip.MustParseAddr("203.0.113.7"))

	// A reservation holds its room until it is settled.
	settle, err := l.Reserve(c, UploadBytes, 80)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(c, UploadBytes, 30); err != ErrLimited {
		t.Errorf("second reservation while the first is open: %v", err)
	}
	// Settling gives back what was not used, once.
	settle(20)
	settle(0)
	if n := l.Count(c, UploadBytes); n != 20 {
		t.Errorf("count after settling: %d, want 20", n)
	}
	// Used above the reservation counts only the reservation.
	settle, _ = l.Reserve(c, UploadBytes, 10)
	settle(1000)
	if n := l.Count(c, UploadBytes); n != 30 {
		t.Errorf("count after settling too much: %d, want 30", n)
	}

	// Settled in the next hour: the old hour is gone, the day is corrected.
	settle, _ = l.Reserve(c, UploadBytes, 70)
	*now = now.Add(time.Hour)
	settle(0)
	if n := l.Count(c, UploadBytes); n != 0 {
		t.Errorf("new hour: %d", n)
	}
	if l.Allow(c, UploadBytes, 100) != nil {
		t.Error("daily room not given back") // 30 of 150 used today, 0 of 100 this hour
	}

	// Settled on the next day: nothing changes, nothing breaks.
	settle, _ = l.Reserve(c, UploadBytes, 0)
	*now = now.Add(24 * time.Hour)
	settle(0)

	// Counting backwards is refused or ignored.
	if l.Allow(c, UploadBytes, -1) == nil {
		t.Error("negative count allowed")
	}
	if _, err := l.Reserve(c, UploadBytes, -1); err == nil {
		t.Error("negative reservation allowed")
	}
	l.Add(c, UploadBytes, -50)
	if n := l.Count(c, UploadBytes); n != 0 {
		t.Errorf("negative Add counted: %d", n)
	}
}
