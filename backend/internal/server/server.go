// Package server implements the FileBunny HTTP API.
//
// Handlers never log or store the client address; see CLAUDE.md.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/storage"
	"github.com/FileBunny/filebunny/backend/internal/store"
	"github.com/FileBunny/filebunny/backend/internal/wkd"
)

// stallTimeout ends an upload or download that makes no progress for this
// long, so slow clients cannot hold connections open forever.
const stallTimeout = time.Minute

// Server serves the HTTP API.
type Server struct {
	cfg     config.Config
	store   *store.Store
	log     *slog.Logger
	now     func() time.Time
	stall   time.Duration
	mux     *http.ServeMux
	handler http.Handler

	// reserved counts bytes of uploads in progress, so concurrent uploads
	// cannot together exceed cfg.MaxTotalSize.
	mu       sync.Mutex
	reserved int64

	// uploads holds the chunked uploads in progress, by ID.
	upMu    sync.Mutex
	uploads map[string]*pendingUpload

	// wkd is nil when the WKD proxy is switched off; wkdSlots caps the
	// lookups in progress.
	wkd      keyLookup
	wkdSlots chan struct{}

	// remotes are the external storage services offered, by name.
	remotes map[string]*remote
}

// New returns a Server with all routes registered.
func New(cfg config.Config, st *store.Store, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, store: st, log: log, now: time.Now, stall: stallTimeout, mux: http.NewServeMux(),
		uploads: make(map[string]*pendingUpload), wkdSlots: make(chan struct{}, maxWKDLookups)}
	if cfg.WKDProxy {
		s.wkd = wkd.NewClient()
	}
	s.remotes = make(map[string]*remote)
	if cfg.Filebin {
		s.addRemote(storage.NewFilebin())
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/files", s.handleUpload)
	s.mux.HandleFunc("GET /api/v1/files/{id}", s.handleDownload)
	s.mux.HandleFunc("HEAD /api/v1/files/{id}", s.handleInfo)
	s.mux.HandleFunc("DELETE /api/v1/files/{id}", s.handleDelete)
	s.mux.HandleFunc("POST /api/v1/uploads", s.handleCreateUpload)
	s.mux.HandleFunc("PUT /api/v1/uploads/{id}", s.handleChunk)
	s.mux.HandleFunc("POST /api/v1/uploads/{id}/complete", s.handleCompleteUpload)
	s.mux.HandleFunc("DELETE /api/v1/uploads/{id}", s.handleAbortUpload)
	s.mux.HandleFunc("GET /api/v1/features", s.handleFeatures)
	s.mux.HandleFunc("GET /api/v1/storage", s.handleStorage)
	s.mux.HandleFunc("POST /api/v1/wkd", s.handleWKD)
	s.mux.HandleFunc("POST /api/v1/inboxes", s.handleCreateInbox)
	s.mux.HandleFunc("GET /api/v1/inboxes/{id}", s.handleInboxInfo)
	s.mux.HandleFunc("POST /api/v1/inboxes/{id}/files", s.handleInboxUpload)
	s.mux.HandleFunc("GET /api/v1/inboxes/{id}/files", s.handleInboxFiles)
	s.mux.HandleFunc("GET /api/v1/inboxes/{id}/files/{file}", s.handleInboxFile)
	s.mux.HandleFunc("DELETE /api/v1/inboxes/{id}/files/{file}", s.handleDeleteInboxFile)
	s.mux.HandleFunc("DELETE /api/v1/inboxes/{id}", s.handleDeleteInbox)
	s.handler = securityHeaders(nullOriginCORS(s.mux))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

type uploadResponse struct {
	ID          string    `json:"id"`
	DeleteToken string    `json:"delete_token"`
	Size        int64     `json:"size"`
	ExpiresAt   time.Time `json:"expires_at"`
	// MaxDownloads is omitted for unlimited downloads.
	MaxDownloads int `json:"max_downloads,omitempty"`
}

func newUploadResponse(f store.File, token string) uploadResponse {
	return uploadResponse{
		ID:           f.ID,
		DeleteToken:  token,
		Size:         f.Size,
		ExpiresAt:    f.ExpiresAt.UTC(),
		MaxDownloads: f.MaxDownloads,
	}
}

// limits are the sharing options of an upload.
type limits struct {
	expiry       time.Duration
	maxDownloads int // 0: unlimited
	// remote is the external service to store at; nil means this server.
	remote *remote
}

// parseLimits reads the optional query parameters "storage", the name of
// an external service from GET /api/v1/storage ("local" or empty: this
// server), "expires", a Go duration (e.g. "1h", "168h") between
// config.MinExpiry and the configured max (or the service's, if lower),
// and "max_downloads", between 1 and the configured max. On error it writes
// the response and returns false.
func (s *Server) parseLimits(w http.ResponseWriter, r *http.Request) (limits, bool) {
	l := limits{expiry: s.cfg.DefaultExpiry}
	q := r.URL.Query()
	maxExpiry := s.cfg.MaxExpiry
	if v := q.Get("storage"); v != "" && v != "local" {
		l.remote = s.remotes[v]
		if l.remote == nil {
			writeError(w, http.StatusBadRequest, "unknown storage")
			return limits{}, false
		}
		// Refuse before the data is sent, not after.
		if healthy, _ := l.remote.status(); !healthy {
			writeRemoteError(w, errRemoteDown)
			return limits{}, false
		}
		maxExpiry = min(maxExpiry, l.remote.MaxExpiry())
		l.expiry = min(l.expiry, maxExpiry)
	}
	if v := q.Get("expires"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < config.MinExpiry || d > maxExpiry {
			writeError(w, http.StatusBadRequest, "expires must be a duration between "+config.MinExpiry.String()+" and "+maxExpiry.String())
			return limits{}, false
		}
		l.expiry = d
	}
	if v := q.Get("max_downloads"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > s.cfg.MaxDownloads {
			writeError(w, http.StatusBadRequest, "max_downloads must be a number between 1 and "+strconv.Itoa(s.cfg.MaxDownloads))
			return limits{}, false
		}
		l.maxDownloads = n
	}
	return l, true
}

// handleUpload stores the raw request body, with the limits from
// parseLimits.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	lim, ok := s.parseLimits(w, r)
	if !ok {
		return
	}
	if r.ContentLength > s.cfg.MaxSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(s.cfg.MaxSize, 10)+" bytes")
		return
	}

	// Without Content-Length, reserve the maximum the body may grow to.
	need := s.cfg.MaxSize
	if r.ContentLength >= 0 {
		need = r.ContentLength
	}
	release, err := s.reserve(r.Context(), need)
	if errors.Is(err, errStorageFull) {
		writeError(w, http.StatusInsufficientStorage, "storage is full, try again later")
		return
	}
	if err != nil {
		s.log.Error("upload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	defer release()

	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxSize)
	rc := http.NewResponseController(w)
	now := s.now()
	var f store.File
	var token string
	if lim.remote == nil {
		f, token, err = s.store.Put(r.Context(), stallReader{body, rc, s.stall}, now, now.Add(lim.expiry), lim.maxDownloads)
	} else {
		f, token, err = s.putRemote(r.Context(), stallReader{body, rc, s.stall}, lim)
	}
	if writeRemoteError(w, err) {
		return
	}
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(s.cfg.MaxSize, 10)+" bytes")
			return
		}
		// The client stalled or went away; the connection is usually gone,
		// but answer in case it is not. Normal enough not to log.
		if _, ok := errors.AsType[bodyError](err); ok || r.Context().Err() != nil {
			s.log.Debug("upload aborted", "err", err)
			writeError(w, http.StatusBadRequest, "upload aborted")
			return
		}
		s.log.Error("upload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}

	writeJSON(w, http.StatusCreated, newUploadResponse(f, token))
}

var errStorageFull = errors.New("storage full")

// reserve claims n bytes of the storage quota for an upload in progress.
// The caller must call release when the upload is done, stored or not.
func (s *Server) reserve(ctx context.Context, n int64) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.store.TotalSize(ctx)
	if err != nil {
		return nil, err
	}
	if used+s.reserved+n > s.cfg.MaxTotalSize {
		return nil, errStorageFull
	}
	s.reserved += n
	return func() {
		s.mu.Lock()
		s.reserved -= n
		s.mu.Unlock()
	}, nil
}

// handleInfo answers HEAD with the download headers, without counting a
// download, so the download page can show expiry and downloads left.
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Stat(r.Context(), r.PathValue("id"), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("info failed", "err", err)
		writeError(w, http.StatusInternalServerError, "info failed")
		return
	}
	setFileHeaders(w, f)
}

// handleDownload counts a download, also when the client aborts it: counting
// at the end would let parallel downloads of a one-time file all succeed.
// For a file at an external service, a transfer is claimed first, so a busy
// or failing service does not use up a download.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var rem *remote
	if f, err := s.store.Stat(r.Context(), id, s.now()); err == nil && f.Storage != "" {
		if rem = s.remotes[f.Storage]; rem == nil {
			writeError(w, http.StatusServiceUnavailable, "external storage is no longer offered")
			return
		}
		release, err := rem.acquire(f.Size, s.cfg.ProxyDailyBytes, s.now())
		if writeRemoteError(w, err) {
			return
		}
		defer release()
	}
	f, blob, err := s.store.Get(r.Context(), id, s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("download failed", "err", err)
		writeError(w, http.StatusInternalServerError, "download failed")
		return
	}
	if blob == nil {
		if rem == nil { // only if the file appeared between Stat and Get
			writeError(w, http.StatusServiceUnavailable, "try again")
			return
		}
		s.serveRemote(w, r, f, rem)
		return
	}
	defer blob.Close()

	setFileHeaders(w, f)
	sw := stallWriter{w, http.NewResponseController(w), s.stall}
	if _, err := io.Copy(sw, blob); err != nil && r.Context().Err() == nil {
		s.log.Warn("download interrupted", "err", err)
	}
}

// setFileHeaders describes f. Downloads-Remaining is omitted when downloads
// are unlimited; on a download it is the number left after this one.
func setFileHeaders(w http.ResponseWriter, f store.File) {
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	h.Set("Content-Disposition", "attachment")
	h.Set("Cache-Control", "no-store")
	h.Set("Expires-At", f.ExpiresAt.UTC().Format(http.TimeFormat))
	if left := f.DownloadsLeft(); left >= 0 {
		h.Set("Downloads-Remaining", strconv.Itoa(left))
	}
}

// handleDelete expects the delete token as "Authorization: Bearer <token>".
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing delete token")
		return
	}
	err := s.store.Delete(r.Context(), r.PathValue("id"), token, s.now())
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "wrong delete token")
	default:
		s.log.Error("delete failed", "err", err)
		writeError(w, http.StatusInternalServerError, "delete failed")
	}
}

// stallReader moves the read deadline forward before every read, so a body
// may take as long as it needs as long as data keeps arriving.
type stallReader struct {
	r       io.Reader
	rc      *http.ResponseController
	timeout time.Duration
}

func (s stallReader) Read(p []byte) (int, error) {
	_ = s.rc.SetReadDeadline(time.Now().Add(s.timeout)) // unsupported in tests
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		// Keep MaxBytesError unwrappable; mark everything else as the
		// client's fault (timeout, reset) rather than a server error.
		if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
			err = bodyError{err}
		}
	}
	return n, err
}

// bodyError is a failure reading the request body.
type bodyError struct{ err error }

func (e bodyError) Error() string { return "reading body: " + e.err.Error() }
func (e bodyError) Unwrap() error { return e.err }

// stallWriter is the write-side counterpart of stallReader.
type stallWriter struct {
	w       io.Writer
	rc      *http.ResponseController
	timeout time.Duration
}

func (s stallWriter) Write(p []byte) (int, error) {
	_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
	return s.w.Write(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
