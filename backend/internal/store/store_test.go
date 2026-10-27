package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func put(t *testing.T, s *Store, body string, expires time.Time) (File, string) {
	t.Helper()
	f, token, err := s.Put(context.Background(), strings.NewReader(body), t0, expires, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f, token
}

func TestPutGet(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, token := put(t, s, "ciphertext", t0.Add(time.Hour))

	if !idPattern.MatchString(f.ID) {
		t.Errorf("id %q does not match pattern", f.ID)
	}
	if len(token) != 43 {
		t.Errorf("token length %d, want 43", len(token))
	}
	if f.Size != int64(len("ciphertext")) {
		t.Errorf("size %d", f.Size)
	}

	got, blob, err := s.Get(ctx, f.ID, t0)
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Close()
	data, _ := io.ReadAll(blob)
	if string(data) != "ciphertext" || got.Size != f.Size || !got.ExpiresAt.Equal(f.ExpiresAt) {
		t.Errorf("got %+v %q", got, data)
	}
}

func TestIDsAreUnique(t *testing.T) {
	s, _ := openTest(t)
	seen := map[string]bool{}
	for range 50 {
		f, _ := put(t, s, "x", t0.Add(time.Hour))
		if seen[f.ID] {
			t.Fatalf("duplicate id %s", f.ID)
		}
		seen[f.ID] = true
	}
}

func TestGetExpiredOrUnknown(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, _ := put(t, s, "x", t0.Add(time.Hour))

	for name, tc := range map[string]struct {
		id  string
		now time.Time
	}{
		"expired":      {f.ID, t0.Add(time.Hour)},
		"unknown":      {"AAAAAAAAAAAAAAAAAAAAAA", t0},
		"path":         {"../filebunny.db", t0},
		"wrong length": {f.ID + "A", t0},
		"empty":        {"", t0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := s.Get(ctx, tc.id, tc.now); !errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	f, token := put(t, s, "x", t0.Add(time.Hour))

	if err := s.Delete(ctx, f.ID, "wrong", t0); !errors.Is(err, ErrForbidden) {
		t.Errorf("wrong token: err = %v", err)
	}
	if err := s.Delete(ctx, f.ID, token, t0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", f.ID)); !os.IsNotExist(err) {
		t.Errorf("blob still on disk: %v", err)
	}
	if err := s.Delete(ctx, f.ID, token, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v", err)
	}
}

func TestDeleteExpired(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	old, _ := put(t, s, "old", t0.Add(time.Hour))
	fresh, _ := put(t, s, "fresh", t0.Add(48*time.Hour))

	n, err := s.DeleteExpired(ctx, t0.Add(2*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", old.ID)); !os.IsNotExist(err) {
		t.Error("expired blob still on disk")
	}
	if _, blob, err := s.Get(ctx, fresh.ID, t0.Add(2*time.Hour)); err != nil {
		t.Errorf("fresh file gone: %v", err)
	} else {
		blob.Close()
	}
}

func TestRemoveOrphans(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	kept, _ := put(t, s, "kept", t0.Add(time.Hour))
	os.WriteFile(filepath.Join(dir, "blobs", "orphan"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, "tmp", "upload-123"), []byte("x"), 0o600)

	n, err := s.RemoveOrphans(ctx)
	if err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", kept.ID)); err != nil {
		t.Errorf("kept blob removed: %v", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := put(t, s, "x", t0.Add(time.Hour))
	s.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, blob, err := s.Get(context.Background(), f.ID, t0)
	if err != nil {
		t.Fatal(err)
	}
	blob.Close()
}

func TestPutReaderError(t *testing.T) {
	s, dir := openTest(t)
	r := io.MultiReader(strings.NewReader("partial"), errReader{})
	if _, _, err := s.Put(context.Background(), r, t0, t0.Add(time.Hour), 0); err == nil {
		t.Fatal("expected error")
	}
	for _, sub := range []string{"tmp", "blobs"} {
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		if len(entries) != 0 {
			t.Errorf("%s not empty after failed upload: %v", sub, entries)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestDeleteExpiredFileIsNotFound(t *testing.T) {
	s, _ := openTest(t)
	f, token := put(t, s, "x", t0.Add(time.Hour))
	if err := s.Delete(context.Background(), f.ID, token, t0.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestTotalSize(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if n, err := s.TotalSize(ctx); err != nil || n != 0 {
		t.Fatalf("empty: %d, %v", n, err)
	}
	put(t, s, "abc", t0.Add(time.Hour))
	f, token := put(t, s, "defgh", t0.Add(time.Hour))
	if n, _ := s.TotalSize(ctx); n != 8 {
		t.Errorf("total %d, want 8", n)
	}
	s.Delete(ctx, f.ID, token, t0)
	if n, _ := s.TotalSize(ctx); n != 3 {
		t.Errorf("after delete: total %d, want 3", n)
	}
}

func TestDownloadLimit(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	f, _, err := s.Put(ctx, strings.NewReader("twice"), t0, t0.Add(time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if f.MaxDownloads != 2 || f.DownloadsLeft() != 2 {
		t.Fatalf("new file: %+v", f)
	}

	for want := 1; want >= 0; want-- {
		got, blob, err := s.Get(ctx, f.ID, t0)
		if err != nil {
			t.Fatalf("download with %d left: %v", want+1, err)
		}
		// The handle stays readable even after the last download removed
		// the blob.
		if b, _ := io.ReadAll(blob); string(b) != "twice" {
			t.Errorf("body %q", b)
		}
		blob.Close()
		if got.DownloadsLeft() != want {
			t.Errorf("downloads left %d, want %d", got.DownloadsLeft(), want)
		}
	}
	if _, _, err := s.Get(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("third download: err = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", f.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("blob not removed after last download: %v", err)
	}
	if n, _ := s.TotalSize(ctx); n != 0 {
		t.Errorf("total size %d after last download", n)
	}
}

// Concurrent downloads of a one-time file: exactly one succeeds.
func TestDownloadLimitConcurrent(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, _, err := s.Put(ctx, strings.NewReader("once"), t0, t0.Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var ok atomic.Int32
	for range 20 {
		wg.Go(func() {
			if _, blob, err := s.Get(ctx, f.ID, t0); err == nil {
				blob.Close()
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d downloads succeeded, want 1", ok.Load())
	}
}

func TestStatDoesNotCount(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, _, err := s.Put(ctx, strings.NewReader("x"), t0, t0.Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		got, err := s.Stat(ctx, f.ID, t0)
		if err != nil || got.DownloadsLeft() != 1 || got.Size != 1 {
			t.Fatalf("stat: %+v, %v", got, err)
		}
	}
	if _, blob, err := s.Get(ctx, f.ID, t0); err != nil {
		t.Fatal(err)
	} else {
		blob.Close()
	}
	if _, err := s.Stat(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("stat after last download: %v", err)
	}
	if _, err := s.Stat(ctx, "AAAAAAAAAAAAAAAAAAAAAA", t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("stat unknown: %v", err)
	}
}

func TestUnlimitedDownloads(t *testing.T) {
	s, _ := openTest(t)
	f, _ := put(t, s, "x", t0.Add(time.Hour))
	if f.DownloadsLeft() != -1 {
		t.Errorf("downloads left %d, want -1", f.DownloadsLeft())
	}
	for range 5 {
		_, blob, err := s.Get(context.Background(), f.ID, t0)
		if err != nil {
			t.Fatal(err)
		}
		blob.Close()
	}
}

func TestPendingAppendRetry(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	p, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Append(strings.NewReader("part1-")); err != nil {
		t.Fatal(err)
	}
	// A part that fails halfway is discarded completely.
	if _, err := p.Append(io.MultiReader(strings.NewReader("par"), errReader{})); err == nil {
		t.Fatal("expected error")
	}
	if p.Size() != 6 {
		t.Errorf("size after failed part %d, want 6", p.Size())
	}
	if _, err := p.Append(strings.NewReader("part2")); err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Commit(ctx, p, t0, t0.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	_, blob, err := s.Get(ctx, f.ID, t0)
	if err != nil {
		t.Fatal(err)
	}
	defer blob.Close()
	if b, _ := io.ReadAll(blob); string(b) != "part1-part2" || f.Size != 11 {
		t.Errorf("body %q, size %d", b, f.Size)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(entries) != 0 {
		t.Errorf("tmp not empty: %v", entries)
	}
}

func TestPendingAbort(t *testing.T) {
	s, dir := openTest(t)
	p, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	p.Append(strings.NewReader("data"))
	p.Abort()
	if entries, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(entries) != 0 {
		t.Errorf("tmp not empty after abort: %v", entries)
	}
}

// A database created by the first migration keeps its files, which get
// unlimited downloads.
func TestMigrateFromVersion1(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "filebunny.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + `; PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	id := "AAAAAAAAAAAAAAAAAAAAAA"
	if _, err := db.Exec(`INSERT INTO files VALUES (?, ?, 1, ?, ?)`, id, hashToken("t"), t0.Unix(), t0.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	os.MkdirAll(filepath.Join(dir, "blobs"), 0o700)
	os.WriteFile(filepath.Join(dir, "blobs", id), []byte("x"), 0o600)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, err := s.Stat(context.Background(), id, t0)
	if err != nil || f.DownloadsLeft() != -1 || f.Downloads != 0 {
		t.Errorf("migrated file: %+v, %v", f, err)
	}
}
