package store

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func putRemote(t *testing.T, s *Store, ref string, maxDownloads int) (File, string) {
	t.Helper()
	f, token, err := s.CommitRemote(context.Background(), 100, "filebin", ref, t0, t0.Add(time.Hour), maxDownloads)
	if err != nil {
		t.Fatal(err)
	}
	return f, token
}

func due(t *testing.T, s *Store, now time.Time) []RemoteObject {
	t.Helper()
	got, err := s.RemoteDue(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRemoteGetHasNoBlob(t *testing.T) {
	s, _ := openTest(t)
	f, _ := putRemote(t, s, "bin1", 0)

	got, blob, err := s.Get(context.Background(), f.ID, t0)
	if err != nil {
		t.Fatal(err)
	}
	if blob != nil {
		t.Error("remote file returned a local blob")
	}
	if got.Storage != "filebin" || got.RemoteRef != "bin1" || got.Size != 100 {
		t.Errorf("got %+v", got)
	}
}

// The last allowed download removes the row at once, but the remote blob
// stays until the download has had time to finish.
func TestRemoteLastDownloadWaitsForGrace(t *testing.T) {
	s, _ := openTest(t)
	f, _ := putRemote(t, s, "bin1", 1)

	if _, _, err := s.Get(context.Background(), f.ID, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(context.Background(), f.ID, t0); err != ErrNotFound {
		t.Errorf("second download: %v, want ErrNotFound", err)
	}
	if got := due(t, s, t0.Add(downloadGrace-time.Second)); len(got) != 0 {
		t.Errorf("due before the grace period: %v", got)
	}
	want := []RemoteObject{{Storage: "filebin", Ref: "bin1"}}
	if got := due(t, s, t0.Add(downloadGrace)); !reflect.DeepEqual(got, want) {
		t.Errorf("due after the grace period: %v, want %v", got, want)
	}
}

func TestRemoteDeleteAndExpiryAreDueAtOnce(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a, token := putRemote(t, s, "deleted", 0)
	putRemote(t, s, "expired", 0)

	if err := s.Delete(ctx, a.ID, token, t0); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeleteExpired(ctx, t0.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("DeleteExpired: %d, %v", n, err)
	}
	got := due(t, s, t0.Add(time.Hour))
	if len(got) != 2 {
		t.Fatalf("due: %v, want both", got)
	}
	for _, o := range got {
		if err := s.RemoteDeleted(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	if got := due(t, s, t0.Add(time.Hour)); len(got) != 0 {
		t.Errorf("still due after RemoteDeleted: %v", got)
	}
}

// Remote blobs use no disk here, so they must not fill the quota.
func TestTotalSizeCountsOnlyLocal(t *testing.T) {
	s, _ := openTest(t)
	put(t, s, "local", t0.Add(time.Hour))
	putRemote(t, s, "bin1", 0)

	total, err := s.TotalSize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len("local")) {
		t.Errorf("total %d, want %d", total, len("local"))
	}
}

func TestPendingContent(t *testing.T) {
	s, _ := openTest(t)
	p, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Abort()
	for _, part := range []string{"cipher", "text"} {
		if _, err := p.Append(strings.NewReader(part)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := p.Content()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	if string(data) != "ciphertext" {
		t.Errorf("content %q", data)
	}
}
