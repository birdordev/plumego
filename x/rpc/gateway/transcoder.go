// Package gateway adapts caller-owned RPC HTTP handlers to net/http.
package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
)

// HandlerFunc is a dependency-free RPC-to-HTTP handler shape.
type HandlerFunc func(http.ResponseWriter, *http.Request, map[string]string)

// Option configures HTTPTranscoder construction.
type Option func(*HTTPTranscoder)

type HTTPTranscoder struct {
	mux    *http.ServeMux
	target string
}

func New(target string, opts ...Option) *HTTPTranscoder {
	t := &HTTPTranscoder{
		mux:    http.NewServeMux(),
		target: strings.TrimSpace(target),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t
}

func (t *HTTPTranscoder) Register(ctx context.Context, handler HandlerFunc, pattern string) error {
	_ = ctx
	if handler == nil {
		return contract.ErrHandlerNil
	}
	method, path := splitPattern(pattern)
	t.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handler(w, r, nil)
	})
	return nil
}

func (t *HTTPTranscoder) Handler() http.Handler {
	return t.mux
}

func (t *HTTPTranscoder) Target() string {
	return t.target
}

func splitPattern(pattern string) (string, string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return http.MethodGet, "/"
	}
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return http.MethodGet, pattern
	}
	method = strings.TrimSpace(method)
	path = strings.TrimSpace(path)
	if method == "" {
		method = http.MethodGet
	}
	if path == "" {
		path = "/"
	}
	return method, path
}
