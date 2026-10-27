package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	f, token, err := s.Put(context.Background(), strings.NewReader(body), t0, expires)
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

	if err := s.Delete(ctx, f.ID, "wrong"); !errors.Is(err, ErrForbidden) {
		t.Errorf("wrong token: err = %v", err)
	}
	if err := s.Delete(ctx, f.ID, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", f.ID)); !os.IsNotExist(err) {
		t.Errorf("blob still on disk: %v", err)
	}
	if err := s.Delete(ctx, f.ID, token); !errors.Is(err, ErrNotFound) {
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
	if _, _, err := s.Put(context.Background(), r, t0, t0.Add(time.Hour)); err == nil {
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
