// Package gateway adapts caller-owned RPC HTTP handlers to net/http.
package gateway

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/birdordev/plumego/contract"
)

// HandlerFunc is a dependency-free RPC-to-HTTP handler shape.
type HandlerFunc func(http.ResponseWriter, *http.Request, map[string]string)

// Option configures HTTPTranscoder construction.
type Option func(*HTTPTranscoder)

type HTTPTranscoder struct {
	mux    *http.ServeMux
	target string

	mu     sync.RWMutex
	routes map[string]map[string]HandlerFunc
}

func New(target string, opts ...Option) *HTTPTranscoder {
	t := &HTTPTranscoder{
		mux:    http.NewServeMux(),
		target: strings.TrimSpace(target),
		routes: make(map[string]map[string]HandlerFunc),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t
}

// Register adds a handler for a method and path. Patterns without a method
// use GET. Each registered method, including HEAD, is matched explicitly.
// Registration is local and ignores its context argument. At dispatch, the
// handler receives the HTTP request and its request context.
func (t *HTTPTranscoder) Register(_ context.Context, handler HandlerFunc, pattern string) error {
	if handler == nil {
		return contract.ErrHandlerNil
	}
	method, path := splitPattern(pattern)
	if _, err := http.NewRequest(method, "/", nil); err != nil {
		return fmt.Errorf("rpc gateway: invalid method: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	methods := t.routes[path]
	if methods == nil {
		if err := registerMuxPath(t.mux, path, func(w http.ResponseWriter, r *http.Request) {
			t.servePath(w, r, path)
		}); err != nil {
			return err
		}
		methods = make(map[string]HandlerFunc)
		t.routes[path] = methods
	}
	if methods[method] != nil {
		return fmt.Errorf("rpc gateway: %s %s is already registered", method, path)
	}
	methods[method] = handler
	return nil
}

func registerMuxPath(mux *http.ServeMux, path string, handler http.HandlerFunc) (err error) {
	// ServeMux reports invalid and conflicting patterns by panicking.
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("rpc gateway: register %q: %v", path, cause)
		}
	}()
	mux.HandleFunc(path, handler)
	return nil
}

func (t *HTTPTranscoder) servePath(w http.ResponseWriter, r *http.Request, path string) {
	t.mu.RLock()
	methods := t.routes[path]
	handler := methods[r.Method]
	if handler != nil {
		t.mu.RUnlock()
		handler(w, r, nil)
		return
	}
	allowed := slices.Sorted(maps.Keys(methods))
	t.mu.RUnlock()
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
