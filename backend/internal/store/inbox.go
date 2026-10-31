package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"os"
	"time"
)

// Inbox is an upload link: anyone with the link can upload ciphertext for
// the owner of PublicKey, within the limits; only the holder of the
// management token can list, fetch and delete what arrived.
type Inbox struct {
	ID string
	// PublicKey is what the owner gave (armored); the browser of an
	// uploader checks its fingerprint against the link.
	PublicKey  []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	MaxUploads int
	Uploads    int
	MaxBytes   int64
	Bytes      int64
}

// UploadsLeft and BytesLeft never go below zero.
func (in Inbox) UploadsLeft() int { return max(in.MaxUploads-in.Uploads, 0) }
func (in Inbox) BytesLeft() int64 { return max(in.MaxBytes-in.Bytes, 0) }

// Open reports whether the link still accepts uploads.
func (in Inbox) Open(now time.Time) bool {
	return now.Before(in.ExpiresAt) && in.UploadsLeft() > 0 && in.BytesLeft() > 0
}

const inboxColumns = `public_key, created_at, expires_at, max_uploads, uploads, max_bytes, bytes`

// querier is *sql.DB or *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CreateInbox stores a new upload link and returns it with its management
// token; only a hash of the token is stored. At most maxOpen links may be
// unexpired at once (ErrTooManyInboxes); the check and the insert are one
// statement, so parallel requests cannot overshoot it.
func (s *Store) CreateInbox(ctx context.Context, publicKey []byte, now, expiresAt time.Time, maxUploads int, maxBytes int64, maxOpen int) (Inbox, string, error) {
	id := newRandom(idBytes)
	token := newRandom(tokenBytes)
	now, expiresAt = now.Truncate(time.Second), expiresAt.Truncate(time.Second)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO inboxes (id, manage_token_hash, public_key, created_at, expires_at, max_uploads, max_bytes)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE (SELECT COUNT(*) FROM inboxes WHERE expires_at > ?) < ?`,
		id, hashToken(token), publicKey, now.Unix(), expiresAt.Unix(), maxUploads, maxBytes, now.Unix(), maxOpen)
	if err != nil {
		return Inbox{}, "", err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Inbox{}, "", err
	} else if n == 0 {
		return Inbox{}, "", ErrTooManyInboxes
	}
	return Inbox{ID: id, PublicKey: publicKey, CreatedAt: now, ExpiresAt: expiresAt, MaxUploads: maxUploads, MaxBytes: maxBytes}, token, nil
}

// OpenInbox returns an unexpired upload link, also when it is full, so the
// upload page can say so. Expired and unknown links are ErrNotFound.
func (s *Store) OpenInbox(ctx context.Context, id string, now time.Time) (Inbox, error) {
	if !idPattern.MatchString(id) {
		return Inbox{}, ErrNotFound
	}
	return s.openInbox(ctx, s.db, id, now)
}

func (s *Store) openInbox(ctx context.Context, q querier, id string, now time.Time) (Inbox, error) {
	return scanInbox(id, q.QueryRowContext(ctx,
		`SELECT `+inboxColumns+` FROM inboxes WHERE id = ? AND expires_at > ?`, id, now.Unix()))
}

// ManageInbox returns an upload link if token is its management token,
// also after it expired, as long as it still holds files (cleanup removes
// it after that). ErrForbidden for a wrong token.
func (s *Store) ManageInbox(ctx context.Context, id, token string) (Inbox, error) {
	if !idPattern.MatchString(id) {
		return Inbox{}, ErrNotFound
	}
	var stored []byte
	err := s.db.QueryRowContext(ctx, `SELECT manage_token_hash FROM inboxes WHERE id = ?`, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Inbox{}, ErrNotFound
	}
	if err != nil {
		return Inbox{}, err
	}
	if subtle.ConstantTimeCompare(stored, hashToken(token)) != 1 {
		return Inbox{}, ErrForbidden
	}
	return scanInbox(id, s.db.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM inboxes WHERE id = ?`, id))
}

func scanInbox(id string, row *sql.Row) (Inbox, error) {
	in := Inbox{ID: id}
	var created, expires int64
	err := row.Scan(&in.PublicKey, &created, &expires, &in.MaxUploads, &in.Uploads, &in.MaxBytes, &in.Bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return Inbox{}, ErrNotFound
	}
	if err != nil {
		return Inbox{}, err
	}
	in.CreatedAt, in.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
	return in, nil
}

// InboxFiles lists the unexpired files of an upload link, oldest first.
// The caller has checked the management token.
func (s *Store) InboxFiles(ctx context.Context, inboxID string, now time.Time) ([]File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, `+fileColumns+` FROM files WHERE inbox_id = ? AND expires_at > ? ORDER BY created_at, id`,
		inboxID, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []File{}
	for rows.Next() {
		var f File
		var created, expires int64
		var maxDownloads sql.NullInt64
		if err := rows.Scan(&f.ID, &f.Size, &created, &expires, &f.Downloads, &maxDownloads); err != nil {
			return nil, err
		}
		f.CreatedAt, f.ExpiresAt = time.Unix(created, 0), time.Unix(expires, 0)
		files = append(files, f)
	}
	return files, rows.Err()
}

// InboxFile opens an unexpired file of an upload link. Downloads are not
// counted: the owner may fetch it as often as needed. The caller has
// checked the management token and must close the handle.
func (s *Store) InboxFile(ctx context.Context, inboxID, fileID string, now time.Time) (File, *os.File, error) {
	if !idPattern.MatchString(fileID) {
		return File{}, nil, ErrNotFound
	}
	f, err := scanFile(fileID, s.db.QueryRowContext(ctx,
		`SELECT `+fileColumns+` FROM files WHERE id = ? AND inbox_id = ? AND expires_at > ?`,
		fileID, inboxID, now.Unix()))
	if err != nil {
		return File{}, nil, err
	}
	blob, err := os.Open(s.blobPath(fileID))
	if errors.Is(err, os.ErrNotExist) {
		return File{}, nil, ErrNotFound
	}
	if err != nil {
		return File{}, nil, err
	}
	return f, blob, nil
}

// DeleteInboxFile removes one file of an upload link. The upload it used
// stays used. The caller has checked the management token.
func (s *Store) DeleteInboxFile(ctx context.Context, inboxID, fileID string) error {
	if !idPattern.MatchString(fileID) {
		return ErrNotFound
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM files WHERE id = ? AND inbox_id = ?`, fileID, inboxID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.remove(ctx, fileID)
}

// DeleteInbox revokes an upload link: its files go first, then the link,
// so nothing can be uploaded to it any more. The caller has checked the
// management token.
func (s *Store) DeleteInbox(ctx context.Context, inboxID string) error {
	// Close it first, so no upload can commit while the files are removed.
	if _, err := s.db.ExecContext(ctx, `UPDATE inboxes SET expires_at = 0 WHERE id = ?`, inboxID); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM files WHERE inbox_id = ?`, inboxID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.remove(ctx, id); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM inboxes WHERE id = ?`, inboxID)
	return err
}
