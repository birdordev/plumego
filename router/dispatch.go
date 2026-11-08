package router

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/spcent/plumego/contract"
)

// noBodyWriter tracks the representation without sending its body. Final headers
// stay private until completion or flush so implicit status, content sniffing,
// and small-response Content-Length remain consistent with net/http.
type noBodyWriter struct {
	http.ResponseWriter
	status    int
	header    http.Header
	bytes     int64
	committed bool
}

func (w *noBodyWriter) WriteHeader(status int) {
	if w.status != 0 || w.committed {
		return
	}
	if status < 100 || status > 999 {
		panic("invalid WriteHeader code")
	}
	if status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
	w.header = w.Header().Clone()
}

func (w *noBodyWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.bodyAllowed() {
		return 0, http.ErrBodyNotAllowed
	}
	w.bytes += int64(len(p))
	if length, err := strconv.ParseInt(w.header.Get("Content-Length"), 10, 64); err == nil && length >= 0 && w.bytes > length {
		return 0, http.ErrContentLength
	}
	_, hasContentType := w.header["Content-Type"]
	if !w.committed && len(p) > 0 && !hasContentType && w.header.Get("Content-Encoding") == "" {
		w.header.Set("Content-Type", http.DetectContentType(p))
	}
	return len(p), nil
}

func (w *noBodyWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{w}, r)
}

func (w *noBodyWriter) bodyAllowed() bool {
	return w.status >= 200 && w.status != http.StatusNoContent && w.status != http.StatusNotModified
}

func (w *noBodyWriter) commit(inferLength bool) {
	if w.committed {
		return
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if inferLength && w.bodyAllowed() && w.bytes <= 2048 && w.header.Get("Content-Length") == "" && w.header.Get("Transfer-Encoding") == "" {
		w.header.Set("Content-Length", strconv.FormatInt(w.bytes, 10))
	}
	dst := w.Header()
	clear(dst)
	for key, values := range w.header {
		dst[key] = append([]string(nil), values...)
	}
	w.ResponseWriter.WriteHeader(w.status)
	w.committed = true
}

func (w *noBodyWriter) FlushError() error {
	w.commit(false)
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *noBodyWriter) Flush() { _ = w.FlushError() }

func (w *noBodyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.committed = true
	}
	return conn, rw, err
}

func (w *noBodyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ServeHTTP implements http.Handler and handles incoming HTTP requests.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !r.ready() || req == nil {
		http.Error(w, "router not initialized", http.StatusServiceUnavailable)
		return
	}
	if req.URL == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	path := fastNormalizePath(req.URL.Path)
	result := r.cachedMatch(req.Method, path)
	if result == nil {
		if r.state.methodNotAllowed.Load() {
			allowed := r.allowedMethods(path)
			if len(allowed) > 0 {
				w.Header().Set("Allow", strings.Join(allowed, ", "))
				_ = contract.WriteError(w, req, contract.NewErrorBuilder().
					Type(contract.TypeMethodNotAllowed).
					Message("method not allowed").
					Build())
				return
			}
		}
		http.NotFound(w, req)
		return
	}

	w = responseWriterForMatch(w, req, result)
	if head, ok := w.(*noBodyWriter); ok {
		defer func() {
			if recovered := recover(); recovered != nil {
				// Preserve logical commitment for outer recovery middleware.
				if head.status != 0 {
					head.commit(true)
				}
				panic(recovered)
			}
		}()
	}
	r.attachRouteContextAndServe(w, req, result)
	if head, ok := w.(*noBodyWriter); ok {
		head.commit(true)
	}
}

func responseWriterForMatch(w http.ResponseWriter, req *http.Request, result *matchResult) http.ResponseWriter {
	if req != nil && req.Method == http.MethodHead && result != nil {
		return &noBodyWriter{ResponseWriter: w}
	}
	return w
}

func (r *Router) cachedMatch(method, path string) *matchResult {
	// Keep lookup and publication in the same read lock so registration cannot
	// clear the cache between matching a route and publishing the result.
	// Freeze publishes the immutable trie and cache pointer under the write lock.
	if !r.state.frozen.Load() {
		r.state.mu.RLock()
		defer r.state.mu.RUnlock()
	}

	cache := r.state.matchCache
	key := method + ":" + path
	if result, ok := cache.Get(key); ok {
		return result
	}
	result := r.matchRoute(method, path)
	if result != nil {
		// Build metadata before publication. Contexts retain this immutable
		// match even after eviction, including workers behind inner timeouts.
		result.routeState.SetPattern(result.RoutePattern)
		result.routeState.SetName(result.RouteName)
		for i, key := range result.ParamKeys {
			if i < len(result.ParamValues) {
				result.routeState.AddParam(key, result.ParamValues[i])
			}
		}
		cache.Set(key, result)
	}
	return result
}

// matchRoute reads the trie under the caller's read lock or after Freeze.
func (r *Router) matchRoute(method, path string) *matchResult {
	if path == "/" {
		if method != MethodAny {
			tree := r.state.trees[method]
			if tree != nil && tree.handler != nil {
				return &matchResult{
					Handler:      tree.handler,
					RoutePattern: "/",
					RouteMethod:  method,
					RouteName:    tree.routeName,
				}
			}
		}
		// RFC 7231 §4.3.2: HEAD is identical to GET except no body.
		if method == http.MethodHead {
			if getTree := r.state.trees[http.MethodGet]; getTree != nil && getTree.handler != nil {
				return &matchResult{
					Handler:      getTree.handler,
					RoutePattern: "/",
					RouteMethod:  http.MethodGet,
					RouteName:    getTree.routeName,
				}
			}
		}
		if anyTree := r.state.trees[MethodAny]; anyTree != nil && anyTree.handler != nil {
			return &matchResult{
				Handler:      anyTree.handler,
				RoutePattern: "/",
				RouteMethod:  MethodAny,
				RouteName:    anyTree.routeName,
			}
		}
		return nil
	}

	partsPtr := splitPathToParts(path)
	parts := *partsPtr
	defer putPathParts(partsPtr)

	if method != MethodAny {
		tree := r.state.trees[method]
		if tree != nil {
			result := matchNode(tree, parts, path)
			if result != nil {
				result.RouteMethod = method
				return result
			}
		}
	}

	// RFC 7231 §4.3.2: fall back to GET handler for HEAD requests.
	if method == http.MethodHead {
		if getTree := r.state.trees[http.MethodGet]; getTree != nil {
			result := matchNode(getTree, parts, path)
			if result != nil {
				result.RouteMethod = http.MethodGet
				return result
			}
		}
	}

	// Fall back to ANY handler.
	if anyTree := r.state.trees[MethodAny]; anyTree != nil {
		result := matchNode(anyTree, parts, path)
		if result != nil {
			result.RouteMethod = MethodAny
			return result
		}
	}

	return nil
}

// attachRouteContextAndServe shares immutable routing metadata with the request.
// Its context keeps the match alive beyond cache eviction or an inner timeout.
func (r *Router) attachRouteContextAndServe(w http.ResponseWriter, req *http.Request, result *matchResult) {
	if result == nil {
		return
	}
	ctx := contract.WithRouteState(req.Context(), &result.routeState)
	result.Handler.ServeHTTP(w, req.WithContext(ctx))
}

func (r *Router) allowedMethods(path string) []string {
	normalized := path
	if normalized == "" {
		normalized = "/"
	}

	r.state.mu.RLock()
	defer r.state.mu.RUnlock()

	allowed := make([]string, 0, len(r.state.trees))
	if normalized == "/" {
		for method, tree := range r.state.trees {
			if method == MethodAny || tree == nil {
				continue
			}
			if tree.handler != nil {
				allowed = append(allowed, method)
			}
		}
	} else {
		partsPtr := splitPathToParts(normalized)
		parts := *partsPtr

		for method, tree := range r.state.trees {
			if method == MethodAny || tree == nil {
				continue
			}
			if matchNode(tree, parts, normalized) != nil {
				allowed = append(allowed, method)
			}
		}

		putPathParts(partsPtr)
	}

	if len(allowed) > 1 {
		sort.Strings(allowed)
	}
	allowed = appendImplicitHeadMethod(allowed)
	return allowed
}

func appendImplicitHeadMethod(methods []string) []string {
	hasGet := false
	hasHead := false
	for _, method := range methods {
		switch method {
		case http.MethodGet:
			hasGet = true
		case http.MethodHead:
			hasHead = true
		}
	}
	if !hasGet || hasHead {
		return methods
	}
	methods = append(methods, http.MethodHead)
	sort.Strings(methods)
	return methods
}
