package frontend

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// statusCodeWriter wraps http.ResponseWriter to enforce a specific HTTP status
// code when serving custom error or not-found pages. http.ServeContent always
// writes 200 (or 206/304), so this wrapper intercepts WriteHeader and replaces
// any 2xx code with the desired error status while passing through 3xx and
// other codes unchanged.
type statusCodeWriter struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusCodeWriter) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func (s *statusCodeWriter) WriteHeader(code int) {
	if !s.wroteHeader {
		s.wroteHeader = true
		if code >= 200 && code < 300 {
			code = s.code
		}
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusCodeWriter) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(s.code)
	}
	return s.ResponseWriter.Write(b)
}

// serveNotFound serves a custom 404 page or falls back to http.NotFound.
// When a custom page is configured it is served with a 404 status code
// (not 200) via statusCodeWriter.
func (h *handler) serveNotFound(w http.ResponseWriter, r *http.Request) {
	if h.cfg.NotFoundPage != "" {
		sw := &statusCodeWriter{ResponseWriter: w, code: http.StatusNotFound}
		if served, _ := h.serveFileWithPolicy(sw, r, h.cfg.NotFoundPage, false); served {
			return
		}
	}
	http.NotFound(w, r)
}

// serveInternalError serves a custom 500 page or falls back to JSON when
// that page cannot be loaded.
func (h *handler) serveInternalError(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ErrorPage != "" {
		sw := &statusCodeWriter{ResponseWriter: w, code: http.StatusInternalServerError}
		if served, _ := h.serveFileWithPolicy(sw, r, h.cfg.ErrorPage, false); served {
			return
		}
	}
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Code(contract.CodeInternalError).
		Message("internal server error").
		Build())
}

func (h *handler) serveNotAcceptable(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept-Encoding")
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotAcceptable).
		Message("acceptable content encoding not available").
		Build())
}
