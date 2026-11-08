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

func (s *statusCodeWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
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

// serveError serves a custom 5xx error page or falls back to a JSON error response.
// When a custom page is configured it is served with the given error status code
// (not 200) via statusCodeWriter. Errors from loading the error page itself are
// ignored to avoid recursion; the JSON fallback is used instead.
func (h *handler) serveError(w http.ResponseWriter, r *http.Request, message string, code int) {
	if h.cfg.ErrorPage != "" && code >= 500 {
		sw := &statusCodeWriter{ResponseWriter: w, code: code}
		if served, _ := h.serveFileWithPolicy(sw, r, h.cfg.ErrorPage, false); served {
			return
		}
	}
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(frontendErrorType(code)).
		Code(contract.CodeInternalError).
		Message(message).
		Build())
}

func frontendErrorType(code int) contract.ErrorType {
	switch code {
	case http.StatusNotFound:
		return contract.TypeNotFound
	case http.StatusNotAcceptable:
		return contract.TypeNotAcceptable
	default:
		if code >= http.StatusInternalServerError {
			return contract.TypeInternal
		}
		return contract.TypeBadRequest
	}
}

func (h *handler) serveNotAcceptable(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept-Encoding")
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotAcceptable).
		Message("acceptable content encoding not available").
		Build())
}
