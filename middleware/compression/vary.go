package compression

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	internaltransport "github.com/birdordev/plumego/internal/httputil"
)

// encodingVaryWriter adds Vary when the final response commits. Placing it
// outside gzip also covers identity responses and downstream Vary overrides.
type encodingVaryWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *encodingVaryWriter) prepare() {
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
	internaltransport.AddVary(header, "Accept-Encoding")
}

func (w *encodingVaryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *encodingVaryWriter) WriteHeader(status int) {
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.prepare()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *encodingVaryWriter) Write(p []byte) (int, error) {
	w.prepare()
	return w.ResponseWriter.Write(p)
}

func (w *encodingVaryWriter) ReadFrom(r io.Reader) (int64, error) {
	w.prepare()
	return io.Copy(w.ResponseWriter, r)
}

func (w *encodingVaryWriter) FlushError() error {
	wasCommitted := w.committed
	w.prepare()
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		w.committed = wasCommitted
	}
	return err
}

func (w *encodingVaryWriter) Flush() { _ = w.FlushError() }

func (w *encodingVaryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if errors.Is(err, http.ErrNotSupported) {
		return nil, nil, http.ErrNotSupported
	}
	if err == nil {
		w.committed = true
	}
	return conn, rw, err
}

func (w *encodingVaryWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}
