package httputil

import (
	"bytes"
	"net/http"
	"strings"
)

// BufferedResponseRecorder captures response data without writing through.
//
// Use this when middleware must inspect or replace the response before any
// bytes are committed to the original ResponseWriter. Use ResponseRecorder when
// callers need write-through recording instead.
type BufferedResponseRecorder struct {
	responseWriter http.ResponseWriter
	statusCode     int
	header         http.Header
	committed      http.Header
	body           *bytes.Buffer
	bytesWritten   int
	written        bool
	maxBytes       int
	overflow       bool
}

// BufferedResponse captures headers, status, and body in memory.
// It does not write to an underlying ResponseWriter.
type BufferedResponse = BufferedResponseRecorder

// NewBufferedResponseRecorder creates a non-forwarding response recorder.
func NewBufferedResponseRecorder(w http.ResponseWriter) *BufferedResponseRecorder {
	return newBufferedResponseRecorder(w, 0)
}

// NewBufferedResponse creates a non-forwarding response recorder with an
// optional body size limit. A maxBytes value of 0 disables the limit.
func NewBufferedResponse(maxBytes int) *BufferedResponseRecorder {
	return newBufferedResponseRecorder(nil, maxBytes)
}

func newBufferedResponseRecorder(w http.ResponseWriter, maxBytes int) *BufferedResponseRecorder {
	return &BufferedResponseRecorder{
		responseWriter: w,
		statusCode:     http.StatusOK,
		header:         make(http.Header),
		body:           &bytes.Buffer{},
		maxBytes:       maxBytes,
	}
}

// Wrapped returns the original writer, if one was supplied.
func (r *BufferedResponseRecorder) Wrapped() http.ResponseWriter {
	if r == nil {
		return nil
	}
	return r.responseWriter
}

// Header returns the recorded header map.
func (r *BufferedResponseRecorder) Header() http.Header {
	return r.header
}

// WriteHeader records the status code without committing it to the wrapped writer.
func (r *BufferedResponseRecorder) WriteHeader(code int) {
	if r.written {
		return
	}
	// Informational responses cannot be replayed as a final response. Ignore
	// them while preserving a later final status, including protocol switches.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		return
	}
	r.statusCode = code
	r.written = true
	r.committed = r.header.Clone()
}

// Write records body bytes without writing through to the wrapped writer.
func (r *BufferedResponseRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	if r.maxBytes > 0 && r.body.Len()+len(b) > r.maxBytes {
		r.overflow = true
		return 0, http.ErrBodyNotAllowed
	}
	n, err := r.body.Write(b)
	r.bytesWritten += n
	return n, err
}

// StatusCode returns the recorded status code.
func (r *BufferedResponseRecorder) StatusCode() int {
	if r == nil || r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

// Body returns the recorded response body bytes.
func (r *BufferedResponseRecorder) Body() []byte {
	if r == nil || r.body == nil {
		return nil
	}
	return r.body.Bytes()
}

// BytesWritten returns the number of body bytes captured.
func (r *BufferedResponseRecorder) BytesWritten() int {
	if r == nil {
		return 0
	}
	return r.bytesWritten
}

// Overflowed reports whether a write exceeded the configured maxBytes limit.
func (r *BufferedResponseRecorder) Overflowed() bool {
	return r != nil && r.overflow
}

// Len returns the current captured body size in bytes.
func (r *BufferedResponseRecorder) Len() int {
	if r == nil || r.body == nil {
		return 0
	}
	return r.body.Len()
}

// ClearBody clears the captured response body and captured byte count.
func (r *BufferedResponseRecorder) ClearBody() {
	if r == nil || r.body == nil {
		return
	}
	r.body.Reset()
	r.bytesWritten = 0
}

// WriteTo writes the buffered response to dst, replacing destination headers.
func (r *BufferedResponseRecorder) WriteTo(dst http.ResponseWriter) (int, error) {
	if r == nil || dst == nil {
		return 0, nil
	}
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	ReplaceHeaders(dst.Header(), r.committed)
	EnsureNoSniff(dst.Header())
	dst.WriteHeader(r.StatusCode())
	n, err := SafeWrite(dst, r.Body())
	// Trailer values are the only headers allowed to change after commitment.
	for _, declaration := range r.committed.Values("Trailer") {
		for _, name := range strings.Split(declaration, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			if values, ok := r.header[name]; ok {
				dst.Header()[name] = append([]string(nil), values...)
			}
		}
	}
	for name, values := range r.header {
		if strings.HasPrefix(name, http.TrailerPrefix) {
			dst.Header()[name] = append([]string(nil), values...)
		}
	}
	return n, err
}
