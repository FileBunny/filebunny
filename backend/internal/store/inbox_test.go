package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----\n"

func createInbox(t *testing.T, s *Store, maxUploads int, maxBytes int64) (Inbox, string) {
	t.Helper()
	in, token, err := s.CreateInbox(context.Background(), []byte(testKey), t0, t0.Add(24*time.Hour), maxUploads, maxBytes, 100)
	if err != nil {
		t.Fatal(err)
	}
	return in, token
}

func toInbox(s *Store, inboxID, body string, now time.Time) (File, error) {
	p, err := s.Begin()
	if err != nil {
		return File{}, err
	}
	if _, err := p.Append(strings.NewReader(body)); err != nil {
		p.Abort()
		return File{}, err
	}
	return s.CommitToInbox(context.Background(), p, inboxID, now, now.Add(7*24*time.Hour))
}

func TestInboxCreateAndManage(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	in, token := createInbox(t, s, 3, 1000)
	if !idPattern.MatchString(in.ID) || len(token) != 43 {
		t.Fatalf("id %q, token %q", in.ID, token)
	}

	got, err := s.OpenInbox(ctx, in.ID, t0)
	if err != nil || string(got.PublicKey) != testKey || got.UploadsLeft() != 3 || got.BytesLeft() != 1000 || !got.Open(t0) {
		t.Fatalf("OpenInbox: %+v, %v", got, err)
	}
	if _, err := s.ManageInbox(ctx, in.ID, token); err != nil {
		t.Errorf("ManageInbox with token: %v", err)
	}
	if _, err := s.ManageInbox(ctx, in.ID, "wrong"); !errors.Is(err, ErrForbidden) {
		t.Errorf("ManageInbox with wrong token: %v", err)
	}
	for _, id := range []string{"unknown", "AAAAAAAAAAAAAAAAAAAAAA", "../../etc/passwd"} {
		if _, err := s.OpenInbox(ctx, id, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("OpenInbox(%q): %v", id, err)
		}
		if _, err := s.ManageInbox(ctx, id, token); !errors.Is(err, ErrNotFound) {
			t.Errorf("ManageInbox(%q): %v", id, err)
		}
	}
	// Expired: not open for uploads, still manageable.
	if _, err := s.OpenInbox(ctx, in.ID, t0.Add(25*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenInbox after expiry: %v", err)
	}
}

func TestInboxUploadLimits(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	in, _ := createInbox(t, s, 2, 10)

	if _, err := toInbox(s, in.ID, "123456", t0); err != nil {
		t.Fatal(err)
	}
	// Too many bytes: 6 + 5 > 10.
	if _, err := toInbox(s, in.ID, "12345", t0); !errors.Is(err, ErrInboxFull) {
		t.Errorf("over the byte limit: %v", err)
	}
	if _, err := toInbox(s, in.ID, "1234", t0); err != nil {
		t.Fatalf("exactly at the byte limit: %v", err)
	}
	// Too many uploads.
	in2, _ := createInbox(t, s, 1, 1000)
	toInbox(s, in2.ID, "a", t0)
	if _, err := toInbox(s, in2.ID, "b", t0); !errors.Is(err, ErrInboxFull) {
		t.Errorf("over the upload limit: %v", err)
	}
	// Expired or unknown link.
	in3, _ := createInbox(t, s, 5, 1000)
	if _, err := toInbox(s, in3.ID, "x", t0.Add(25*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired link: %v", err)
	}
	if _, err := toInbox(s, "unknown", "x", t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown link: %v", err)
	}
	got, _ := s.OpenInbox(ctx, in.ID, t0)
	if got.Uploads != 2 || got.Bytes != 10 || got.Open(t0) {
		t.Errorf("counters %+v", got)
	}
	// Refused uploads leave nothing behind.
	entries, _ := os.ReadDir(s.tmpDir)
	if len(entries) != 0 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestInboxUploadLimitConcurrent(t *testing.T) {
	s, _ := openTest(t)
	in, _ := createInbox(t, s, 3, 1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 20 {
		wg.Go(func() {
			if _, err := toInbox(s, in.ID, "x", t0); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if ok != 3 {
		t.Errorf("%d uploads accepted, want 3", ok)
	}
	files, _ := s.InboxFiles(context.Background(), in.ID, t0)
	if len(files) != 3 {
		t.Errorf("%d files stored", len(files))
	}
}

// Files of an upload link are not reachable by ID alone.
func TestInboxFilesAreNotPublic(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	in, token := createInbox(t, s, 5, 1000)
	f, err := toInbox(s, in.ID, "secret ciphertext", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.Stat(ctx, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat: %v", err)
	}
	if err := s.Delete(ctx, f.ID, token, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete with the management token: %v", err)
	}
	// Another link cannot reach it either.
	other, _ := createInbox(t, s, 5, 1000)
	if _, _, err := s.InboxFile(ctx, other.ID, f.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("InboxFile via another link: %v", err)
	}
	if err := s.DeleteInboxFile(ctx, other.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteInboxFile via another link: %v", err)
	}
}

func TestInboxListFetchDelete(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	in, _ := createInbox(t, s, 5, 1000)
	a, _ := toInbox(s, in.ID, "first", t0)
	b, _ := toInbox(s, in.ID, "second", t0.Add(time.Minute))

	files, err := s.InboxFiles(ctx, in.ID, t0.Add(time.Hour))
	if err != nil || len(files) != 2 || files[0].ID != a.ID || files[1].ID != b.ID || files[1].Size != 6 {
		t.Fatalf("InboxFiles: %+v, %v", files, err)
	}
	for range 2 { // not counted: can be fetched again
		_, blob, err := s.InboxFile(ctx, in.ID, a.ID, t0)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(blob)
		blob.Close()
		if string(body) != "first" {
			t.Errorf("body %q", body)
		}
	}
	if err := s.DeleteInboxFile(ctx, in.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", a.ID)); !os.IsNotExist(err) {
		t.Errorf("blob still there: %v", err)
	}
	// A deleted upload stays used.
	got, _ := s.OpenInbox(ctx, in.ID, t0)
	if got.Uploads != 2 {
		t.Errorf("uploads %d after delete", got.Uploads)
	}

	if err := s.DeleteInbox(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", b.ID)); !os.IsNotExist(err) {
		t.Errorf("blob of revoked link still there: %v", err)
	}
	if _, err := s.OpenInbox(ctx, in.ID, t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked link still open: %v", err)
	}
	if _, err := toInbox(s, in.ID, "late", t0); !errors.Is(err, ErrNotFound) {
		t.Errorf("upload to revoked link: %v", err)
	}
}

func TestInboxCap(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for range 2 {
		if _, _, err := s.CreateInbox(ctx, []byte(testKey), t0, t0.Add(time.Hour), 1, 10, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateInbox(ctx, []byte(testKey), t0, t0.Add(time.Hour), 1, 10, 2); !errors.Is(err, ErrTooManyInboxes) {
		t.Errorf("third link: %v", err)
	}
	// Expired links do not count.
	if _, _, err := s.CreateInbox(ctx, []byte(testKey), t0.Add(2*time.Hour), t0.Add(3*time.Hour), 1, 10, 2); err != nil {
		t.Errorf("after the others expired: %v", err)
	}
}

// An expired link stays while it holds files, then cleanup removes it.
func TestInboxCleanup(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	empty, _ := createInbox(t, s, 5, 1000)
	full, token := createInbox(t, s, 5, 1000)
	p, _ := s.Begin()
	p.Append(strings.NewReader("x"))
	if _, err := s.CommitToInbox(ctx, p, full.ID, t0, t0.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeleteExpired(ctx, t0.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManageInbox(ctx, empty.ID, "irrelevant"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired empty link kept: %v", err)
	}
	if _, err := s.ManageInbox(ctx, full.ID, token); err != nil {
		t.Errorf("expired link with a file removed too early: %v", err)
	}
	if n, err := s.DeleteExpired(ctx, t0.Add(49*time.Hour)); err != nil || n != 1 {
		t.Fatalf("DeleteExpired: %d, %v", n, err)
	}
	if _, err := s.ManageInbox(ctx, full.ID, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("link kept after its last file expired: %v", err)
	}
}
