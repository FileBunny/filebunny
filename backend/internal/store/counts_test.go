package store

import (
	"context"
	"testing"
	"time"
)

func TestDailyCounts(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()

	f, _ := put(t, s, "a", t0.Add(48*time.Hour))
	put(t, s, "b", t0.Add(48*time.Hour))
	if _, _, err := s.CommitRemote(ctx, 1, "filebin", "ref", t0, t0.Add(time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, blob, err := s.Get(ctx, f.ID, t0)
		if err != nil {
			t.Fatal(err)
		}
		blob.Close()
	}
	in, _ := createInbox(t, s, 10, 1000)
	g, err := toInbox(s, in.ID, "c", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, blob, err := s.InboxFile(ctx, in.ID, g.ID, t0); err != nil {
		t.Fatal(err)
	} else {
		blob.Close()
	}
	// The next UTC day is a row of its own.
	put(t, s, "d", t0.Add(72*time.Hour))
	nextDay := t0.Add(24 * time.Hour)
	if _, _, err := s.CommitRemote(ctx, 1, "filebin", "ref2", nextDay, nextDay.Add(time.Hour), 0); err != nil {
		t.Fatal(err)
	}

	got, err := s.DailyCounts(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := []DailyCount{
		{Day: "2026-10-27", Uploads: 4, InboxUploads: 1, Downloads: 2, InboxDownloads: 1},
		{Day: "2026-10-28", Uploads: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// Failed downloads are not counted.
	if _, _, err := s.Get(ctx, "AAAAAAAAAAAAAAAAAAAAAA", t0); err == nil {
		t.Fatal("unknown ID found")
	}
	if got, _ := s.DailyCounts(ctx, t0); got[0].Downloads != 2 {
		t.Errorf("a failed download was counted: %+v", got[0])
	}

	// Cleanup keeps 400 days, then adds them up per month.
	if _, err := s.DeleteExpired(ctx, t0.Add(countsKeep+24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DailyCounts(ctx, t0); len(got) != 1 || got[0].Day != "2026-10-28" {
		t.Errorf("days after cleanup: %+v", got)
	}
	months, err := s.MonthlyCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 1 || months[0] != (DailyCount{Day: "2026-10", Uploads: 4, InboxUploads: 1, Downloads: 2, InboxDownloads: 1}) {
		t.Errorf("months after the first cleanup: %+v", months)
	}
	// The next roll-up adds to the same month, and running it again changes nothing.
	for range 2 {
		if _, err := s.DeleteExpired(ctx, t0.Add(countsKeep+48*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	months, _ = s.MonthlyCounts(ctx)
	if len(months) != 1 || months[0] != (DailyCount{Day: "2026-10", Uploads: 5, InboxUploads: 1, Downloads: 2, InboxDownloads: 1}) {
		t.Errorf("months after the second cleanup: %+v", months)
	}
	if got, _ := s.DailyCounts(ctx, t0); len(got) != 0 {
		t.Errorf("days left: %+v", got)
	}
}
