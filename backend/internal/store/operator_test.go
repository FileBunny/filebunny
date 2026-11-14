package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveFileWhateverItsState(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	// Expired, but not cleaned up yet: no visitor can reach it, the
	// operator can.
	f, _ := put(t, s, "ciphertext", t0.Add(time.Minute))

	d, err := s.FileDetails(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Size != int64(len("ciphertext")) || d.InboxID != "" || !d.ExpiresAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("details = %+v", d)
	}
	if err := s.RemoveFile(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileDetails(ctx, f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after remove: %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs", f.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("blob still there: %v", err)
	}
	if err := s.RemoveFile(ctx, f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: %v, want ErrNotFound", err)
	}
}

func TestRemoveFileOfAnUploadLink(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	in, _ := createInbox(t, s, 5, 1<<20)
	f, err := toInbox(s, in.ID, "ciphertext", t0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.FileDetails(ctx, f.ID)
	if err != nil || d.InboxID != in.ID {
		t.Fatalf("details = %+v, %v; want inbox %s", d, err, in.ID)
	}
	if err := s.RemoveFile(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	// The link itself stays; only that file is gone.
	di, err := s.InboxDetails(ctx, in.ID)
	if err != nil || di.Files != 0 {
		t.Errorf("inbox = %+v, %v; want it with 0 files", di, err)
	}
}

func TestRemoveInboxWithItsFiles(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	in, _ := createInbox(t, s, 5, 1<<20)
	var ids []string
	for range 2 {
		f, err := toInbox(s, in.ID, "ciphertext", t0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	d, err := s.InboxDetails(ctx, in.ID)
	if err != nil || d.Files != 2 || d.Uploads != 2 {
		t.Fatalf("details = %+v, %v", d, err)
	}
	if err := s.RemoveInbox(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InboxDetails(ctx, in.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("inbox after remove: %v", err)
	}
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(dir, "blobs", id)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("blob %s still there: %v", id, err)
		}
	}
}

func TestRemoveRemoteFileGoesToTrashAtOnce(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	f, _, err := s.CommitRemote(ctx, 100, "filebin", "bin/name", t0, t0.Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFile(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	due, err := s.RemoteDue(ctx, t0)
	if err != nil || len(due) != 1 {
		t.Errorf("remote due = %v, %v; want the blob, due now", due, err)
	}
}

func TestOperatorRejectsBadIDs(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for _, id := range []string{"", "../../etc/passwd", "short", "AAAAAAAAAAAAAAAAAAAAA/"} {
		if _, err := s.FileDetails(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("FileDetails(%q) = %v", id, err)
		}
		if err := s.RemoveInbox(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("RemoveInbox(%q) = %v", id, err)
		}
	}
}
