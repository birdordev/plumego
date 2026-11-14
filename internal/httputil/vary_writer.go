package httputil

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// VaryWriter merges request header names into Vary when the response commits.
type VaryWriter struct {
	http.ResponseWriter
	committed bool
	fields    []string
}

// NewVaryWriter wraps w and preserves existing Vary values.
func NewVaryWriter(w http.ResponseWriter, fields ...string) *VaryWriter {
	return &VaryWriter{ResponseWriter: w, fields: fields}
}

// Prepare adds Vary for an implicit response when the handler has returned.
func (w *VaryWriter) Prepare() {
	if w.committed {
		return
	}
	w.committed = true
	header := w.Header()
	for _, line := range header.Values("Vary") {
		for token := range strings.SplitSeq(line, ",") {
			if strings.TrimSpace(token) == "*" {
				return
			}
		}
	}
	AddVary(header, w.fields...)
}

func (w *VaryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *VaryWriter) WriteHeader(status int) {
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.Prepare()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *VaryWriter) Write(p []byte) (int, error) {
	w.Prepare()
	return w.ResponseWriter.Write(p)
}

func (w *VaryWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}

func (w *VaryWriter) FlushError() error {
	wasCommitted := w.committed
	w.Prepare()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		w.committed = wasCommitted
	}
	return err
}

func (w *VaryWriter) Flush() { _ = w.FlushError() }

func (w *VaryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if errors.Is(err, http.ErrNotSupported) {
		return nil, nil, http.ErrNotSupported
	}
	if err == nil {
		w.committed = true
	}
	return conn, rw, err
}

func (w *VaryWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}
