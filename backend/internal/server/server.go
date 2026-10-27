// Package server implements the FileBunny HTTP API.
package server

import (
	"log/slog"
	"net/http"
)

// Server serves the HTTP API.
type Server struct {
	log *slog.Logger
	mux *http.ServeMux
}

// New returns a Server with all routes registered.
func New(log *slog.Logger) *Server {
	s := &Server{log: log, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
