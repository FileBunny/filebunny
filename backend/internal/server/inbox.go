package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

// Upload links (inboxes): the owner registers a public key and gets a
// public link plus a secret management token. Anyone with the link can
// upload ciphertext, within the link's limits; only the token holder can
// list, fetch and delete what arrived. Never log IDs or tokens.
const (
	// defaultInboxUploads applies when a new link asks for no limit.
	defaultInboxUploads = 10
	// maxPublicKeySize caps the stored key. Real keys are a few KiB.
	maxPublicKeySize = 64 << 10
)

var (
	armorBegin = []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")
	armorEnd   = []byte("-----END PGP PUBLIC KEY BLOCK-----")
)

type createInboxResponse struct {
	ID          string    `json:"id"`
	ManageToken string    `json:"manage_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	MaxUploads  int       `json:"max_uploads"`
	MaxSize     int64     `json:"max_size"`
	// KeepSeconds is how long each upload is kept after it arrives.
	KeepSeconds int64 `json:"keep_seconds"`
}

type inboxInfoResponse struct {
	PublicKey   string    `json:"public_key"`
	ExpiresAt   time.Time `json:"expires_at"`
	UploadsLeft int       `json:"uploads_left"`
	BytesLeft   int64     `json:"bytes_left"`
	// MaxFileSize is the most one upload may be now.
	MaxFileSize int64 `json:"max_file_size"`
}

type inboxFile struct {
	ID         string    `json:"id"`
	Size       int64     `json:"size"`
	ReceivedAt time.Time `json:"received_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type manageInboxResponse struct {
	ExpiresAt  time.Time   `json:"expires_at"`
	Open       bool        `json:"open"`
	Uploads    int         `json:"uploads"`
	MaxUploads int         `json:"max_uploads"`
	Bytes      int64       `json:"bytes"`
	MaxBytes   int64       `json:"max_bytes"`
	Files      []inboxFile `json:"files"`
}

type inboxUploadResponse struct {
	Size int64 `json:"size"`
}

// handleCreateInbox registers a public key (ASCII-armored, as the body).
// Query: "expires" (Go duration, default and max from the config) and
// "max_uploads" (1 to the configured max, default 10).
func (s *Server) handleCreateInbox(w http.ResponseWriter, r *http.Request) {
	expiry := s.cfg.InboxDefaultExpiry
	maxUploads := min(defaultInboxUploads, s.cfg.InboxMaxUploads)
	q := r.URL.Query()
	if v := q.Get("expires"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < config.MinExpiry || d > s.cfg.InboxMaxExpiry {
			writeError(w, http.StatusBadRequest, "expires must be a duration between "+config.MinExpiry.String()+" and "+s.cfg.InboxMaxExpiry.String())
			return
		}
		expiry = d
	}
	if v := q.Get("max_uploads"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > s.cfg.InboxMaxUploads {
			writeError(w, http.StatusBadRequest, "max_uploads must be a number between 1 and "+strconv.Itoa(s.cfg.InboxMaxUploads))
			return
		}
		maxUploads = n
	}
	key, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPublicKeySize))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "public key exceeds "+strconv.Itoa(maxPublicKeySize)+" bytes")
		return
	}
	if !looksLikeArmoredPublicKey(key) {
		writeError(w, http.StatusBadRequest, "body must be an ASCII-armored OpenPGP public key")
		return
	}

	now := s.now()
	in, token, err := s.store.CreateInbox(r.Context(), key, now, now.Add(expiry), maxUploads, s.cfg.InboxMaxSize, s.cfg.MaxInboxes)
	if errors.Is(err, store.ErrTooManyInboxes) {
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusServiceUnavailable, "too many upload links, try again later")
		return
	}
	if err != nil {
		s.log.Error("create upload link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "create upload link failed")
		return
	}
	writeJSON(w, http.StatusCreated, createInboxResponse{
		ID: in.ID, ManageToken: token, ExpiresAt: in.ExpiresAt.UTC(), MaxUploads: in.MaxUploads, MaxSize: in.MaxBytes,
		KeepSeconds: int64(s.cfg.MaxExpiry / time.Second),
	})
}

// looksLikeArmoredPublicKey is a cheap shape check, not a parse: the
// server never judges keys (the browser checks the fingerprint), but it
// stores and serves only text that looks like an armored public key.
func looksLikeArmoredPublicKey(b []byte) bool {
	b = bytes.TrimSpace(b)
	if !bytes.HasPrefix(b, armorBegin) || !bytes.HasSuffix(b, armorEnd) {
		return false
	}
	for _, c := range b {
		if (c < 0x20 || c > 0x7e) && c != '\n' && c != '\r' && c != '\t' {
			return false
		}
	}
	return true
}

// handleInboxInfo is what the upload page needs: the key and what is left.
// A full link is still shown (with nothing left), an expired one is gone.
func (s *Server) handleInboxInfo(w http.ResponseWriter, r *http.Request) {
	in, err := s.store.OpenInbox(r.Context(), r.PathValue("id"), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("upload link info failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload link info failed")
		return
	}
	writeJSON(w, http.StatusOK, inboxInfoResponse{
		PublicKey:   string(in.PublicKey),
		ExpiresAt:   in.ExpiresAt.UTC(),
		UploadsLeft: in.UploadsLeft(),
		BytesLeft:   in.BytesLeft(),
		MaxFileSize: s.inboxMaxFileSize(in),
	})
}

func (s *Server) inboxMaxFileSize(in store.Inbox) int64 {
	if in.UploadsLeft() == 0 {
		return 0
	}
	return min(s.cfg.MaxSize, in.BytesLeft())
}

// handleInboxUpload stores the body as one upload to the link. Files of a
// link expire after the configured max expiry, independent of the link.
func (s *Server) handleInboxUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, err := s.store.OpenInbox(r.Context(), id, s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("upload to link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	limit := s.inboxMaxFileSize(in)
	if limit == 0 {
		writeError(w, http.StatusConflict, "this upload link is full")
		return
	}
	if r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(limit, 10)+" bytes")
		return
	}
	need := limit
	if r.ContentLength >= 0 {
		need = r.ContentLength
	}
	release, err := s.reserve(r.Context(), need)
	if errors.Is(err, errStorageFull) {
		writeError(w, http.StatusInsufficientStorage, "storage is full, try again later")
		return
	}
	if err != nil {
		s.log.Error("upload to link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	defer release()

	p, err := s.store.Begin()
	if err != nil {
		s.log.Error("upload to link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	if _, err := p.Append(stallReader{body, http.NewResponseController(w), s.stall}); err != nil {
		p.Abort()
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(limit, 10)+" bytes")
			return
		}
		if _, ok := errors.AsType[bodyError](err); ok || r.Context().Err() != nil {
			s.log.Debug("upload to link aborted", "err", err)
			writeError(w, http.StatusBadRequest, "upload aborted")
			return
		}
		s.log.Error("upload to link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	if p.Size() == 0 {
		p.Abort()
		writeError(w, http.StatusBadRequest, "upload is empty")
		return
	}
	s.commitToInbox(w, r, p, id)
}

// commitToInbox finishes an upload to a link (single or chunked) and
// writes the response. p must not be used after.
func (s *Server) commitToInbox(w http.ResponseWriter, r *http.Request, p *store.Pending, inboxID string) {
	now := s.now()
	f, err := s.store.CommitToInbox(r.Context(), p, inboxID, now, now.Add(s.cfg.MaxExpiry))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrInboxFull):
		writeError(w, http.StatusConflict, "this upload link is full")
	case err != nil:
		s.log.Error("upload to link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
	default:
		writeJSON(w, http.StatusCreated, inboxUploadResponse{Size: f.Size})
	}
}

// manageInbox checks the management token ("Authorization: Bearer").
// On failure it writes the response and returns false.
func (s *Server) manageInbox(w http.ResponseWriter, r *http.Request) (store.Inbox, bool) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing management token")
		return store.Inbox{}, false
	}
	in, err := s.store.ManageInbox(r.Context(), r.PathValue("id"), token)
	switch {
	case err == nil:
		return in, true
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "wrong management token")
	default:
		s.log.Error("manage upload link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "manage upload link failed")
	}
	return store.Inbox{}, false
}

func (s *Server) handleInboxFiles(w http.ResponseWriter, r *http.Request) {
	in, ok := s.manageInbox(w, r)
	if !ok {
		return
	}
	now := s.now()
	files, err := s.store.InboxFiles(r.Context(), in.ID, now)
	if err != nil {
		s.log.Error("list upload link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	resp := manageInboxResponse{
		ExpiresAt: in.ExpiresAt.UTC(), Open: in.Open(now),
		Uploads: in.Uploads, MaxUploads: in.MaxUploads, Bytes: in.Bytes, MaxBytes: in.MaxBytes,
		Files: make([]inboxFile, len(files)),
	}
	for i, f := range files {
		resp.Files[i] = inboxFile{ID: f.ID, Size: f.Size, ReceivedAt: f.CreatedAt.UTC(), ExpiresAt: f.ExpiresAt.UTC()}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleInboxFile(w http.ResponseWriter, r *http.Request) {
	in, ok := s.manageInbox(w, r)
	if !ok {
		return
	}
	f, blob, err := s.store.InboxFile(r.Context(), in.ID, r.PathValue("file"), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("download from link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "download failed")
		return
	}
	defer blob.Close()
	setFileHeaders(w, f)
	sw := stallWriter{w, http.NewResponseController(w), s.stall}
	if _, err := io.Copy(sw, blob); err != nil && r.Context().Err() == nil {
		s.log.Warn("download from link interrupted", "err", err)
	}
}

func (s *Server) handleDeleteInboxFile(w http.ResponseWriter, r *http.Request) {
	in, ok := s.manageInbox(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteInboxFile(r.Context(), in.ID, r.PathValue("file"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.log.Error("delete from link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "delete failed")
	}
}

// handleDeleteInbox revokes the link and deletes everything it received.
func (s *Server) handleDeleteInbox(w http.ResponseWriter, r *http.Request) {
	in, ok := s.manageInbox(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteInbox(r.Context(), in.ID); err != nil {
		s.log.Error("revoke upload link failed", "err", err)
		writeError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
