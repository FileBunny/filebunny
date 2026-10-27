package server

import "net/http"

// securityHeaders sets headers for API responses. Caddy sets HSTS and the
// frontend's CSP; it leaves these API headers alone.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// sandbox: even if a browser rendered a download, no script runs.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// nullOriginCORS lets pages opened from disk use the API: the downloadable
// single HTML file sends "Origin: null". Other origins get no CORS headers,
// so other websites cannot use the API from their visitors' browsers in
// the ordinary way. A sandboxed iframe on any site also has origin null;
// that is acceptable because the API has no cookies or other ambient
// authority, only tokens the caller already holds.
func nullOriginCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Add("Vary", "Origin")
		if r.Header.Get("Origin") != "null" {
			next.ServeHTTP(w, r)
			return
		}
		h.Set("Access-Control-Allow-Origin", "null")
		h.Set("Access-Control-Expose-Headers", "Downloads-Remaining, Expires-At")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
