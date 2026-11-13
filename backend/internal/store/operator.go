package store

import (
	"context"
	"database/sql"
	"errors"
)

// Operator functions, for the admin commands of cmd/filebunny. They run on
// the server only and see files and upload links whatever their state
// (expired, used up), because a takedown or an information request may
// concern one that no visitor can reach any more.

// FileDetails is everything stored about one file.
type FileDetails struct {
	File
	// InboxID is the upload link the file was sent to; empty for a share
	// link.
	InboxID string
	// Notified counts the mails sent for its share link.
	Notified int
}

// InboxDetails is everything stored about one upload link.
type InboxDetails struct {
	Inbox
	// Files counts the files it holds now.
	Files int
}

// FileDetails returns what is stored about file id, or ErrNotFound.
func (s *Store) FileDetails(ctx context.Context, id string) (FileDetails, error) {
	if !idPattern.MatchString(id) {
		return FileDetails{}, ErrNotFound
	}
	f, err := scanFile(id, s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files WHERE id = ?`, id))
	if err != nil {
		return FileDetails{}, err
	}
	d := FileDetails{File: f}
	var inbox sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT inbox_id, notified FROM files WHERE id = ?`, id).Scan(&inbox, &d.Notified); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileDetails{}, ErrNotFound
		}
		return FileDetails{}, err
	}
	d.InboxID = inbox.String
	return d, nil
}

// InboxDetails returns what is stored about upload link id, or ErrNotFound.
func (s *Store) InboxDetails(ctx context.Context, id string) (InboxDetails, error) {
	if !idPattern.MatchString(id) {
		return InboxDetails{}, ErrNotFound
	}
	in, err := scanInbox(id, s.db.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM inboxes WHERE id = ?`, id))
	if err != nil {
		return InboxDetails{}, err
	}
	d := InboxDetails{Inbox: in}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM files WHERE inbox_id = ?`, id).Scan(&d.Files); err != nil {
		return InboxDetails{}, err
	}
	return d, nil
}

// RemoveFile removes file id at once, whatever its state, or returns
// ErrNotFound. A blob at an external service goes to remote_trash, which
// the running server empties.
func (s *Store) RemoveFile(ctx context.Context, id string) error {
	if _, err := s.FileDetails(ctx, id); err != nil {
		return err
	}
	return s.remove(ctx, id, immediately)
}

// RemoveInbox removes upload link id and all its files at once, or returns
// ErrNotFound.
func (s *Store) RemoveInbox(ctx context.Context, id string) error {
	if _, err := s.InboxDetails(ctx, id); err != nil {
		return err
	}
	return s.DeleteInbox(ctx, id)
}
