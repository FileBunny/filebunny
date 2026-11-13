package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/store"
)

var now = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func testEnv(dir string) func(string) string {
	return func(k string) string {
		if k == "FILEBUNNY_DATA_DIR" {
			return dir
		}
		return ""
	}
}

func runAdmin(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := admin(args, &out, &errOut, testEnv(dir), now)
	return code, out.String(), errOut.String()
}

func seed(t *testing.T, dir string) (fileID, inboxID string) {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	f, _, err := st.Put(ctx, strings.NewReader("ciphertext"), now, now.Add(time.Hour), 3)
	if err != nil {
		t.Fatal(err)
	}
	in, _, err := st.CreateInbox(ctx, []byte("key"), "", now, now.Add(time.Hour), 5, 1<<20, 100)
	if err != nil {
		t.Fatal(err)
	}
	return f.ID, in.ID
}

func TestAdminInfoAndRemoveFile(t *testing.T) {
	dir := t.TempDir()
	fileID, _ := seed(t, dir)
	link := "https://filebunny.eu/#/d/" + fileID

	code, out, errOut := runAdmin(t, dir, "info", link)
	if code != 0 || errOut != "" {
		t.Fatalf("info: %d %q", code, errOut)
	}
	for _, want := range []string{"file " + fileID, "share link", "10 bytes", "0 of 3", "this server's disk"} {
		if !strings.Contains(out, want) {
			t.Errorf("info lacks %q:\n%s", want, out)
		}
	}

	if code, out, _ := runAdmin(t, dir, "remove", fileID); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("remove: %d %q", code, out)
	}
	code, _, errOut = runAdmin(t, dir, "info", fileID)
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("info after remove: %d %q", code, errOut)
	}
}

func TestAdminRemoveUploadLink(t *testing.T) {
	dir := t.TempDir()
	_, inboxID := seed(t, dir)
	link := "https://filebunny.eu/#/u/" + inboxID + "/" + strings.Repeat("ab", 20)

	code, out, _ := runAdmin(t, dir, "info", inboxID)
	if code != 0 || !strings.Contains(out, "upload link "+inboxID) || !strings.Contains(out, "0 of 5") {
		t.Fatalf("info: %d %q", code, out)
	}
	code, out, _ = runAdmin(t, dir, "remove", link)
	if code != 0 || !strings.Contains(out, "with its 0 file(s)") {
		t.Fatalf("remove: %d %q", code, out)
	}
}

func TestAdminRefusesTokensAndJunk(t *testing.T) {
	dir := t.TempDir()
	fileID, inboxID := seed(t, dir)
	token := strings.Repeat("A", 43)
	for _, arg := range []string{
		"https://filebunny.eu/#/m/" + inboxID + "/" + token,
		"https://filebunny.eu/#/delete/" + fileID + "/" + token,
		"../../etc/passwd",
		"https://filebunny.eu/#/d/" + fileID + "/extra",
	} {
		code, out, errOut := runAdmin(t, dir, "remove", arg)
		if code != 1 || out != "" {
			t.Errorf("remove %q: %d %q %q", arg, code, out, errOut)
		}
		if strings.Contains(errOut, "AAAA") || strings.Contains(errOut, "filebunny.eu") {
			t.Errorf("error repeats the argument: %q", errOut)
		}
	}
	// Nothing was removed.
	if code, _, _ := runAdmin(t, dir, "info", fileID, inboxID); code != 0 {
		t.Error("a refused argument removed something")
	}
}

func TestAdminUsage(t *testing.T) {
	for _, args := range [][]string{{"remove"}, {"delete", "x"}, {"help"}} {
		code, _, errOut := runAdmin(t, t.TempDir(), args...)
		if code != 2 || !strings.Contains(errOut, "usage:") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
}
