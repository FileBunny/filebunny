package main

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A second copy started next to a running server (docker exec without a
// command) must stop before it touches the data: at startup the server
// removes everything in tmp/, where the running one keeps the uploads in
// progress.
func TestSecondCopyLeavesUploadsInProgress(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	upload := filepath.Join(dir, "tmp", "upload-in-progress")
	if err := os.WriteFile(upload, []byte("part"), 0o600); err != nil {
		t.Fatal(err)
	}
	running, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	t.Setenv("FILEBUNNY_DATA_DIR", dir)
	t.Setenv("FILEBUNNY_LISTEN", running.Addr().String())

	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("second copy started")
	}
	if _, err := os.Stat(upload); err != nil {
		t.Errorf("upload in progress removed: %v", err)
	}
}
