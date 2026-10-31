// Package storage keeps ciphertext at external services ("remotes"), as an
// alternative to this server's disk. The server sends the blob on and
// fetches it back; the service sees this server's address, never the
// client's.
//
// Remote blobs are named by a server-generated random reference; nothing
// from the client ends up in a URL. Hosts are fixed per service.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"time"
)

var (
	// ErrNotFound means the service no longer has the blob.
	ErrNotFound = errors.New("storage: not found")
	// ErrUnavailable means the service failed or refused: network, TLS,
	// timeout, unexpected status, a verification page instead of data.
	ErrUnavailable = errors.New("storage: unavailable")
)

// Remote is an external service that holds blobs.
type Remote interface {
	// Name identifies the service in the API and the database.
	Name() string
	// MaxExpiry is how long a blob may be kept there at most.
	MaxExpiry() time.Duration
	// Upload stores size bytes from r under ref; sum is their SHA-256.
	Upload(ctx context.Context, ref string, r io.Reader, size int64, sum [32]byte) error
	// Download returns the blob, which must be exactly size bytes: the
	// reader fails if it is longer or shorter.
	Download(ctx context.Context, ref string, size int64) (io.ReadCloser, error)
	// Delete removes the blob; a blob that is already gone is no error.
	Delete(ctx context.Context, ref string) error
}

// NewRef returns a random reference of 128 bits, as lowercase hex (the
// most widely accepted form for names at external services).
func NewRef() string {
	b := make([]byte, 16)
	rand.Read(b) // never returns an error since Go 1.24
	return hex.EncodeToString(b)
}

// exactReader passes through exactly size bytes and fails on more or less,
// so a truncated or padded blob is never served as complete.
type exactReader struct {
	r    io.ReadCloser
	left int64
}

var errSize = errors.New("storage: blob has the wrong size")

func (e *exactReader) Read(p []byte) (int, error) {
	if e.left <= 0 {
		// One more byte would be too many.
		var one [1]byte
		if n, _ := e.r.Read(one[:]); n > 0 {
			return 0, errSize
		}
		return 0, io.EOF
	}
	if int64(len(p)) > e.left {
		p = p[:e.left]
	}
	n, err := e.r.Read(p)
	e.left -= int64(n)
	if err == io.EOF && e.left > 0 {
		return n, errSize
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

func (e *exactReader) Close() error { return e.r.Close() }
