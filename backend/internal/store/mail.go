package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrMailFileLimit means the share link was mailed to enough addresses.
	ErrMailFileLimit = errors.New("store: share link mailed too often")
	// ErrMailRecipientLimit means the address got enough mail today.
	ErrMailRecipientLimit = errors.New("store: address got enough mail today")
	// ErrMailDailyLimit means the server sent enough mail today.
	ErrMailDailyLimit = errors.New("store: daily mail limit reached")
)

// MailLimits are the caps ClaimMail enforces.
type MailLimits struct {
	PerFile      int
	PerRecipient int
	Daily        int
}

// pepperFile holds the secret key for address hashes. Losing it only
// resets the counters and the block list; keep it with the database.
const pepperFile = "mail-pepper"

// MailPepper returns the secret key for hashing addresses, created on first
// use in the data directory and readable by this user only.
func (s *Store) MailPepper() ([]byte, error) {
	path := filepath.Join(s.dataDir, pepperFile)
	b, err := os.ReadFile(path)
	if err == nil && len(b) == 32 {
		return b, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		return nil, errors.New("store: mail pepper has the wrong size")
	}
	b = make([]byte, 32)
	rand.Read(b)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	return b, f.Close()
}

func mailDay(now time.Time) int64 { return now.Unix() / 86400 }

// ShareLink returns an available share link if token is its delete token,
// without counting a download: only its sender may have it mailed.
func (s *Store) ShareLink(ctx context.Context, id, token string, now time.Time) (File, error) {
	if !idPattern.MatchString(id) {
		return File{}, ErrNotFound
	}
	var stored []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT delete_token_hash FROM files WHERE id = ? AND `+available, id, now.Unix()).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	if subtle.ConstantTimeCompare(stored, hashToken(token)) != 1 {
		return File{}, ErrForbidden
	}
	return s.Stat(ctx, id, now)
}

// ClaimMail counts one mail of a share link to the address with the given
// hash against all limits, in one transaction, so parallel requests cannot
// exceed them. A claim is not given back if sending fails.
func (s *Store) ClaimMail(ctx context.Context, fileID string, addressHash []byte, lim MailLimits, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	res, err := tx.ExecContext(ctx,
		`UPDATE files SET notified = notified + 1 WHERE id = ? AND notified < ? AND `+available,
		fileID, lim.PerFile, now.Unix())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrMailFileLimit
	}
	day := mailDay(now)
	ok, err := countMail(ctx, tx, "to:"+hex.EncodeToString(addressHash), day, lim.PerRecipient)
	if err != nil {
		return err
	}
	if !ok {
		return ErrMailRecipientLimit
	}
	if ok, err = countMail(ctx, tx, "all", day, lim.Daily); err != nil {
		return err
	}
	if !ok {
		return ErrMailDailyLimit
	}
	return tx.Commit()
}

// ClaimRelay counts one mail through the named relay; false when it has
// sent limit mails today.
func (s *Store) ClaimRelay(ctx context.Context, name string, limit int, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() // no-op after Commit
	ok, err := countMail(ctx, tx, "relay:"+name, mailDay(now), limit)
	if err != nil || !ok {
		return false, err
	}
	return true, tx.Commit()
}

// countMail adds one to key's count for day if it stays within limit.
func countMail(ctx context.Context, tx *sql.Tx, key string, day int64, limit int) (bool, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO mail_counts (key, day, count) VALUES (?, ?, 1)
		ON CONFLICT (key, day) DO UPDATE SET count = count + 1 WHERE count < ?`,
		key, day, limit)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1 && limit > 0, err
}

// BlockMail records that the address with this hash wants no more mail.
func (s *Store) BlockMail(ctx context.Context, addressHash []byte, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO mail_blocks (address_hash, created_at) VALUES (?, ?)`, addressHash, now.Unix())
	return err
}

// UnblockMail undoes BlockMail.
func (s *Store) UnblockMail(ctx context.Context, addressHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mail_blocks WHERE address_hash = ?`, addressHash)
	return err
}

// MailBlocked reports whether the address with this hash wants no mail.
func (s *Store) MailBlocked(ctx context.Context, addressHash []byte) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM mail_blocks WHERE address_hash = ?`, addressHash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
