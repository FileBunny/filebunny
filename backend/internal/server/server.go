// Package server implements the FileBunny HTTP API.
//
// Handlers never log or store the client address; see CLAUDE.md.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FileBunny/filebunny/backend/internal/config"
	"github.com/FileBunny/filebunny/backend/internal/store"
)

// Server serves the HTTP API.
type Server struct {
	cfg   config.Config
	store *store.Store
	log   *slog.Logger
	now   func() time.Time
	mux   *http.ServeMux
}

// New returns a Server with all routes registered.
func New(cfg config.Config, st *store.Store, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, store: st, log: log, now: time.Now, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/files", s.handleUpload)
	s.mux.HandleFunc("GET /api/v1/files/{id}", s.handleDownload)
	s.mux.HandleFunc("DELETE /api/v1/files/{id}", s.handleDelete)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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
}

// handleUpload stores the raw request body. The optional query parameter
// "expires" is a Go duration (e.g. "1h", "168h") up to the configured max.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	expiry := s.cfg.DefaultExpiry
	if v := r.URL.Query().Get("expires"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > s.cfg.MaxExpiry {
			writeError(w, http.StatusBadRequest, "expires must be a duration between 1s and "+s.cfg.MaxExpiry.String())
			return
		}
		expiry = d
	}
	if r.ContentLength > s.cfg.MaxSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(s.cfg.MaxSize, 10)+" bytes")
		return
	}

	body := http.MaxBytesReader(w, r.Body, s.cfg.MaxSize)
	now := s.now()
	f, token, err := s.store.Put(r.Context(), body, now, now.Add(expiry))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds "+strconv.FormatInt(s.cfg.MaxSize, 10)+" bytes")
			return
		}
		if r.Context().Err() != nil {
			return // client went away
		}
		s.log.Error("upload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "upload failed")
		return
	}

	writeJSON(w, http.StatusCreated, uploadResponse{
		ID:          f.ID,
		DeleteToken: token,
		Size:        f.Size,
		ExpiresAt:   f.ExpiresAt.UTC(),
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	f, blob, err := s.store.Get(r.Context(), r.PathValue("id"), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.log.Error("download failed", "err", err)
		writeError(w, http.StatusInternalServerError, "download failed")
		return
	}
	defer blob.Close()

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	h.Set("Content-Disposition", "attachment")
	h.Set("Cache-Control", "no-store")
	h.Set("Expires-At", f.ExpiresAt.UTC().Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, blob); err != nil && r.Context().Err() == nil {
		s.log.Error("download interrupted", "err", err)
	}
}

// handleDelete expects the delete token as "Authorization: Bearer <token>".
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		writeError(w, http.StatusUnauthorized, "missing delete token")
		return
	}
	err := s.store.Delete(r.Context(), r.PathValue("id"), token)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
