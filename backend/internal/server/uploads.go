package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/store"
)

// Chunked uploads let the browser encrypt and send a large file in parts,
// without holding all ciphertext in memory (most browsers cannot stream a
// request body). Sessions live in memory only; after a restart their temp
// files are removed by store.RemoveOrphans.
const (
	// uploadIdleTimeout ends a session that receives no chunk for this
	// long, so abandoned uploads give back their slot, quota and disk.
	uploadIdleTimeout = 2 * time.Minute
	// maxChunkSize caps a single chunk.
	maxChunkSize = 16 << 20 // 16 MiB
)

// pendingUpload is one chunked upload in progress.
type pendingUpload struct {
	id        string
	tokenHash [sha256.Size]byte
	lim       limits

	// mu is held for the whole of a request on this upload; a second
	// request for the same upload at the same time gets 409.
	mu         sync.Mutex
	p          *store.Pending
	done       bool     // committed, aborted or expired
	releases   []func() // quota held by the chunks received so far
	lastActive time.Time
	timer      *time.Timer
}

type createUploadResponse struct {
	ID           string `json:"id"`
	UploadToken  string `json:"upload_token"`
	MaxChunkSize int64  `json:"max_chunk_size"`
	MaxSize      int64  `json:"max_size"`
}

type chunkResponse struct {
	Offset int64 `json:"offset"`
}

// handleCreateUpload starts a chunked upload with the limits from
// parseLimits; they apply when it completes.
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	lim, ok := s.parseLimits(w, r)
	if !ok {
		return
	}

	s.upMu.Lock()
	defer s.upMu.Unlock()
	if len(s.uploads) >= s.cfg.MaxPendingUploads {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable, "too many uploads in progress, try again later")
		return
	}
	p, err := s.store.Begin()
	if err != nil {
		s.log.Error("create upload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "create upload failed")
		return
	}
	token := randomString(32)
	u := &pendingUpload{
		id:         randomString(16),
		tokenHash:  sha256.Sum256([]byte(token)),
		lim:        lim,
		p:          p,
		lastActive: time.Now(),
	}
	u.timer = time.AfterFunc(uploadIdleTimeout, func() { s.expireUpload(u) })
	s.uploads[u.id] = u

	writeJSON(w, http.StatusCreated, createUploadResponse{
		ID:           u.id,
		UploadToken:  token,
		MaxChunkSize: maxChunkSize,
		MaxSize:      s.cfg.MaxSize,
	})
}

// handleChunk appends the body at ?offset=, which must equal the bytes
// received so far. A chunk is all or nothing, so after a failure the client
// sends the same chunk again; a wrong offset gets 409 with the right one.
func (s *Server) handleChunk(w http.ResponseWriter, r *http.Request) {
	u, ok := s.lockUpload(w, r)
	if !ok {
		return
	}
	defer s.unlockUpload(u)

	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "offset must be a non-negative number")
		return
	}
	if offset != u.p.Size() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "wrong offset", "offset": u.p.Size()})
		return
	}
	if r.ContentLength < 0 {
		writeError(w, http.StatusLengthRequired, "Content-Length required")
		return
	}
	if r.ContentLength > maxChunkSize {
		writeError(w, http.StatusRequestEntityTooLarge, "chunk exceeds "+strconv.Itoa(maxChunkSize)+" bytes")
		return
	}
	if u.p.Size()+r.ContentLength > s.cfg.MaxSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(s.cfg.MaxSize, 10)+" bytes")
		return
	}

	release, err := s.reserve(r.Context(), r.ContentLength)
	if errors.Is(err, errStorageFull) {
		writeError(w, http.StatusInsufficientStorage, "storage is full, try again later")
		return
	}
	if err != nil {
		s.log.Error("chunk failed", "err", err)
		writeError(w, http.StatusInternalServerError, "chunk failed")
		return
	}

	body := http.MaxBytesReader(w, r.Body, r.ContentLength)
	if _, err := u.p.Append(stallReader{body, http.NewResponseController(w), s.stall}); err != nil {
		release()
		if _, ok := errors.AsType[bodyError](err); ok || r.Context().Err() != nil {
			s.log.Debug("chunk aborted", "err", err)
			writeError(w, http.StatusBadRequest, "chunk aborted")
			return
		}
		s.log.Error("chunk failed", "err", err)
		writeError(w, http.StatusInternalServerError, "chunk failed")
		return
	}
	u.releases = append(u.releases, release)
	writeJSON(w, http.StatusOK, chunkResponse{Offset: u.p.Size()})
}

// handleCompleteUpload stores the received chunks as a file and answers
// like a single upload.
func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := s.lockUpload(w, r)
	if !ok {
		return
	}
	defer s.unlockUpload(u)

	if u.p.Size() == 0 {
		writeError(w, http.StatusBadRequest, "upload is empty")
		return
	}
	now := s.now()
	f, token, err := s.store.Commit(r.Context(), u.p, now, now.Add(u.lim.expiry), u.lim.maxDownloads)
	// Commit consumes the pending upload, also when it fails.
	s.finishUpload(u)
	if err != nil {
		s.log.Error("complete upload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "complete upload failed")
		return
	}
	writeJSON(w, http.StatusCreated, newUploadResponse(f, token))
}

// handleAbortUpload discards a chunked upload.
func (s *Server) handleAbortUpload(w http.ResponseWriter, r *http.Request) {
	u, ok := s.lockUpload(w, r)
	if !ok {
		return
	}
	defer s.unlockUpload(u)
	u.p.Abort()
	s.finishUpload(u)
	w.WriteHeader(http.StatusNoContent)
}

// lockUpload finds the upload for the path ID, checks the bearer token and
// locks it. On failure it writes the response and returns false.
func (s *Server) lockUpload(w http.ResponseWriter, r *http.Request) (*pendingUpload, bool) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing upload token")
		return nil, false
	}
	s.upMu.Lock()
	u := s.uploads[r.PathValue("id")]
	s.upMu.Unlock()
	if u == nil {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	h := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(h[:], u.tokenHash[:]) != 1 {
		writeError(w, http.StatusForbidden, "wrong upload token")
		return nil, false
	}
	if !u.mu.TryLock() {
		writeError(w, http.StatusConflict, "another request for this upload is in progress")
		return nil, false
	}
	if u.done {
		u.mu.Unlock()
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	// A request in progress is covered by the stall timeout.
	u.timer.Stop()
	return u, true
}

// unlockUpload restarts the idle timer of an upload that is still open.
func (s *Server) unlockUpload(u *pendingUpload) {
	if !u.done {
		u.lastActive = time.Now()
		u.timer.Reset(uploadIdleTimeout)
	}
	u.mu.Unlock()
}

// expireUpload runs from the idle timer.
func (s *Server) expireUpload(u *pendingUpload) {
	u.mu.Lock()
	defer u.mu.Unlock()
	// A request may have been running when the timer fired; it reset the
	// timer when it finished.
	if u.done || time.Since(u.lastActive) < uploadIdleTimeout {
		return
	}
	u.p.Abort()
	s.finishUpload(u)
}

// finishUpload releases the slot and the quota of an upload whose pending
// file was committed or aborted. The caller holds u.mu.
func (s *Server) finishUpload(u *pendingUpload) {
	u.done = true
	u.timer.Stop()
	for _, release := range u.releases {
		release()
	}
	u.releases = nil
	s.upMu.Lock()
	delete(s.uploads, u.id)
	s.upMu.Unlock()
}

func bearerToken(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token, ok && token != ""
}

// randomString returns n random bytes, base64url-encoded.
func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b) // never returns an error since Go 1.24
	return base64.RawURLEncoding.EncodeToString(b)
}
