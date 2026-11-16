package httputil

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// BaseWrappedResponseWriter provides the common ResponseWriter delegation
// boilerplate used by middleware that needs to intercept writes.
//
// Embed it in custom response writer structs and override methods as needed.
// The zero value is usable once ResponseWriter is assigned.
type BaseWrappedResponseWriter struct {
	http.ResponseWriter
	wrote bool
}

// Unwrap returns the underlying response writer for http.ResponseController.
func (w *BaseWrappedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// WriteHeader guards against double-invocation and delegates to the
// underlying writer. Override this when middleware needs additional
// pre-delegate logic (e.g. body limiting or compression decisions).
func (w *BaseWrappedResponseWriter) WriteHeader(statusCode int) {
	if w.wrote {
		return
	}
	if statusCode < 100 || statusCode > 999 {
		panic("invalid WriteHeader code")
	}
	if statusCode >= 200 || statusCode == http.StatusSwitchingProtocols {
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write delegates to the underlying writer after ensuring WriteHeader
// has been called. Override this when middleware needs to intercept body
// bytes.
func (w *BaseWrappedResponseWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

// FlushError forwards Flush and records implicit commitment when supported.
func (w *BaseWrappedResponseWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if !errors.Is(err, http.ErrNotSupported) {
		w.wrote = true
	}
	return err
}

// Flush implements http.Flusher.
func (w *BaseWrappedResponseWriter) Flush() { _ = w.FlushError() }

// Hijack delegates Hijack to the underlying writer when available.
// It marks the writer as written so that callers know no further header
// changes are possible.
func (w *BaseWrappedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := HijackIfSupported(w.ResponseWriter)
	if err != nil {
		return nil, nil, err
	}
	w.wrote = true
	return conn, rw, nil
}

// Written reports whether the response has been committed or hijacked.
func (w *BaseWrappedResponseWriter) Written() bool {
	return w.wrote
}
