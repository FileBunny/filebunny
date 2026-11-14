package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

// Admin commands for the operator, run on the server next to the running
// backend (docker exec). There is no API or button for them: removing a
// file is not something a visitor can ask for (docs/architecture.md). They
// print to the terminal only and log nothing.
const adminUsage = `usage: filebunny info <id or link>...
       filebunny remove <id or link>...

An id or link is a file id, a share link (…/#/d/<id>) or an upload link
(…/#/u/<id>/<fingerprint>). Links with a secret token (management or
delete links) are refused: give only the id from them, so the token does
not end up in the shell history.

info    prints everything this server stores about a file or upload link
remove  removes a file at once, or an upload link with all its files
`

var (
	bareID     = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	shareRoute = regexp.MustCompile(`#/d/([A-Za-z0-9_-]{22})$`)
	inboxRoute = regexp.MustCompile(`#/u/([A-Za-z0-9_-]{22})/(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	tokenRoute = regexp.MustCompile(`#/(?:m|delete|block)/`)
)

// kind is what an argument names: a file, an upload link, or either (a
// bare id, looked up as a file first).
type kind int

const (
	anyKind kind = iota
	fileKind
	inboxKind
)

func parseTarget(arg string) (string, kind, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case bareID.MatchString(arg):
		return arg, anyKind, nil
	case tokenRoute.MatchString(arg):
		return "", 0, errors.New("this link holds a secret token; give only the id from it")
	}
	if m := shareRoute.FindStringSubmatch(arg); m != nil {
		return m[1], fileKind, nil
	}
	if m := inboxRoute.FindStringSubmatch(arg); m != nil {
		return m[1], inboxKind, nil
	}
	return "", 0, errors.New("not a file id, share link or upload link")
}

// isAdmin reports whether the arguments ask for an admin command rather
// than the server.
func isAdmin(args []string) bool { return len(args) > 0 }

// admin runs an admin command and returns the exit code.
func admin(args []string, stdout, stderr io.Writer, getenv func(string) string, now time.Time) int {
	if len(args) < 2 || (args[0] != "info" && args[0] != "remove") {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	cfg, err := config.FromEnv(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "filebunny:", err)
		return 1
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(stderr, "filebunny:", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	code := 0
	for i, arg := range args[1:] {
		id, k, err := parseTarget(arg)
		if err == nil {
			if args[0] == "info" {
				err = info(ctx, st, stdout, id, k, now)
			} else {
				err = remove(ctx, st, stdout, id, k)
			}
		}
		if errors.Is(err, store.ErrNotFound) {
			err = errors.New("not found: it never existed, or it expired or was removed and is gone")
		}
		if err != nil {
			// By position, not by value: the argument may hold a token.
			fmt.Fprintf(stderr, "argument %d: %v\n", i+1, err)
			code = 1
		}
	}
	return code
}

func info(ctx context.Context, st *store.Store, w io.Writer, id string, k kind, now time.Time) error {
	if k != inboxKind {
		d, err := st.FileDetails(ctx, id)
		if err == nil {
			printFile(w, d, now)
			return nil
		}
		if k == fileKind || !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	d, err := st.InboxDetails(ctx, id)
	if err != nil {
		return err
	}
	printInbox(w, d, now)
	return nil
}

func remove(ctx context.Context, st *store.Store, w io.Writer, id string, k kind) error {
	if k != inboxKind {
		d, err := st.FileDetails(ctx, id)
		if err == nil {
			if err := st.RemoveFile(ctx, id); err != nil {
				return err
			}
			fmt.Fprintf(w, "file %s removed\n", id)
			if d.Storage != "" {
				fmt.Fprintf(w, "  its copy at %s (%s) is deleted by the running server within a few minutes\n", d.Storage, d.RemoteRef)
			}
			return nil
		}
		if k == fileKind || !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	d, err := st.InboxDetails(ctx, id)
	if err != nil {
		return err
	}
	if err := st.RemoveInbox(ctx, id); err != nil {
		return err
	}
	fmt.Fprintf(w, "upload link %s removed, with its %d file(s)\n", id, d.Files)
	return nil
}

func printFile(w io.Writer, d store.FileDetails, now time.Time) {
	fmt.Fprintf(w, "file %s\n", d.ID)
	if d.InboxID != "" {
		fmt.Fprintf(w, "  sent to       upload link %s\n", d.InboxID)
	} else {
		fmt.Fprintf(w, "  sent as       share link\n")
	}
	fmt.Fprintf(w, "  size          %d bytes (ciphertext)\n", d.Size)
	fmt.Fprintf(w, "  created       %s\n", stamp(d.CreatedAt))
	fmt.Fprintf(w, "  expires       %s%s\n", stamp(d.ExpiresAt), past(d.ExpiresAt, now))
	limit := "unlimited"
	if d.MaxDownloads > 0 {
		limit = fmt.Sprint(d.MaxDownloads)
	}
	fmt.Fprintf(w, "  downloads     %d of %s\n", d.Downloads, limit)
	fmt.Fprintf(w, "  mails sent    %d\n", d.Notified)
	fmt.Fprintf(w, "  stored at     %s\n", where(d.Storage, d.RemoteRef))
}

func printInbox(w io.Writer, d store.InboxDetails, now time.Time) {
	fmt.Fprintf(w, "upload link %s\n", d.ID)
	fmt.Fprintf(w, "  created       %s\n", stamp(d.CreatedAt))
	fmt.Fprintf(w, "  expires       %s%s\n", stamp(d.ExpiresAt), past(d.ExpiresAt, now))
	fmt.Fprintf(w, "  uploads       %d of %d\n", d.Uploads, d.MaxUploads)
	fmt.Fprintf(w, "  bytes         %d of %d\n", d.Bytes, d.MaxBytes)
	fmt.Fprintf(w, "  files now     %d\n", d.Files)
	fmt.Fprintf(w, "  public key    %d bytes (the owner's, as given; its fingerprint is in the link)\n", len(d.PublicKey))
	fmt.Fprintf(w, "  uploads go to %s\n", where(d.Storage, ""))
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func past(t, now time.Time) string {
	if !now.Before(t) {
		return " (expired)"
	}
	return ""
}

func where(storage, ref string) string {
	if storage == "" {
		return "this server's disk"
	}
	if ref == "" {
		return storage
	}
	return storage + " (" + ref + ")"
}
