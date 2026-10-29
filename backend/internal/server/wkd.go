package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/FileBunny/filebunny/backend/internal/wkd"
)

// maxWKDLookups caps outgoing WKD lookups in progress, server-wide, so
// anonymous clients cannot use the server to open many connections.
// Per-client rate limiting follows in phase 7.
const maxWKDLookups = 8

// keyLookup fetches a key from a Web Key Directory (wkd.Client).
type keyLookup interface {
	Lookup(ctx context.Context, a wkd.Address) ([]byte, error)
}

type featuresResponse struct {
	WKD bool `json:"wkd"`
}

// handleFeatures tells the frontend which optional endpoints are on.
func (s *Server) handleFeatures(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, featuresResponse{WKD: s.wkd != nil})
}

// handleWKD fetches the key for {"email": "..."} from the recipient's
// domain and returns the raw bytes; the browser parses and checks them.
// POST keeps the address out of URLs. Never log the address, its domain,
// hash or the upstream URL.
func (s *Server) handleWKD(w http.ResponseWriter, r *http.Request) {
	if s.wkd == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	addr, err := wkd.ParseAddress(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid e-mail address")
		return
	}

	select {
	case s.wkdSlots <- struct{}{}:
		defer func() { <-s.wkdSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "too many lookups, try again later")
		return
	}

	key, err := s.wkd.Lookup(r.Context(), addr)
	switch {
	case errors.Is(err, wkd.ErrNotFound):
		writeError(w, http.StatusNotFound, "no key published")
		return
	case err != nil:
		// The reason is a fixed category, never the domain or URL.
		s.log.Info("wkd lookup failed", "reason", wkd.Reason(err))
		writeError(w, http.StatusBadGateway, "lookup failed")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(key)))
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(key)
}
