package router

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
)

const headSniffLength = 512 // http.DetectContentType examines at most 512 bytes.

// noBodyWriter tracks the representation without sending its body. Final headers
// stay private until completion or flush so implicit status, content sniffing,
// and small-response Content-Length remain consistent with net/http.
type noBodyWriter struct {
	http.ResponseWriter
	status    int
	header    http.Header
	sniff     []byte
	bytes     int64
	committed bool
	hijacked  bool
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
	if w.hijacked {
		return 0, http.ErrHijacked
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if len(p) == 0 {
		return 0, nil
	}
	if !w.bodyAllowed() {
		return 0, http.ErrBodyNotAllowed
	}
	w.bytes += int64(len(p))
	if length, err := strconv.ParseInt(w.header.Get("Content-Length"), 10, 64); err == nil && length >= 0 && w.bytes > length {
		return 0, http.ErrContentLength
	}
	_, hasContentType := w.header["Content-Type"]
	if !w.committed && len(w.sniff) < headSniffLength && !hasContentType &&
		w.header.Get("Content-Encoding") == "" && w.header.Get("Transfer-Encoding") == "" {
		w.sniff = append(w.sniff, p[:min(len(p), headSniffLength-len(w.sniff))]...)
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
	if len(w.sniff) > 0 {
		w.header.Set("Content-Type", http.DetectContentType(w.sniff))
	}
	_, hasContentLength := w.header["Content-Length"]
	if inferLength && w.bodyAllowed() && w.bytes > 0 && w.bytes <= 2048 && !hasContentLength &&
		w.header.Get("Transfer-Encoding") == "" && !hasResponseTrailers(w.header) {
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

func hasResponseTrailers(header http.Header) bool {
	if len(header.Values("Trailer")) > 0 {
		return true
	}
	for key := range header {
		if strings.HasPrefix(key, http.TrailerPrefix) {
			return true
		}
	}
	return false
}

func (w *noBodyWriter) FlushError() error {
	// Resolve the capability before committing headers, in ResponseController order.
	for dst := w.ResponseWriter; ; {
		switch writer := dst.(type) {
		case interface{ FlushError() error }:
			if w.status == 0 && !w.committed {
				err := writer.FlushError()
				if !errors.Is(err, http.ErrNotSupported) {
					w.WriteHeader(http.StatusOK)
					w.committed = true
				}
				return err
			}
			// A nonempty status must reach the destination before it flushes.
			// FlushError provides no separate probe or rollback for that commit.
			w.commit(false)
			return writer.FlushError()
		case http.Flusher:
			w.commit(false)
			writer.Flush()
			return nil
		case interface{ Unwrap() http.ResponseWriter }:
			dst = writer.Unwrap()
		default:
			return http.ErrNotSupported
		}
	}
}

func (w *noBodyWriter) Flush() { _ = w.FlushError() }

func (w *noBodyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
		w.committed = true
	}
	return conn, rw, err
}

func (w *noBodyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// ServeHTTP implements http.Handler and handles incoming HTTP requests.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req == nil || req.Method != http.MethodHead {
		r.serveHTTP(w, req)
		return
	}

	head := &noBodyWriter{ResponseWriter: w}
	returned := false
	defer func() {
		// 异常退出时保留已选定的状态。
		// 只有正常返回才会提交隐式成功。
		if returned || head.status != 0 {
			head.commit(true)
		}
	}()
	r.serveHTTP(head, req)
	returned = true
}

func (r *Router) serveHTTP(w http.ResponseWriter, req *http.Request) {
	if !r.ready() || req == nil {
		http.Error(w, "router not initialized", http.StatusServiceUnavailable)
		return
	}
	if req.URL == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	escaped := r.state.escapedPathSegments.Load()
	path := req.URL.Path
	if escaped {
		path = req.URL.EscapedPath()
	}
	path = fastNormalizePath(path)
	var parts *[]string
	result := r.cachedMatchParts(req.Method, path, &parts, escaped)
	if parts != nil {
		defer putPathParts(parts)
	}
	if result == nil {
		if r.state.methodNotAllowed.Load() {
			allowed := r.allowedMethods(path, parts, escaped)
			if len(allowed) > 0 {
				w.Header().Set("Allow", strings.Join(allowed, ", "))
				_ = contract.WriteError(w, req, contract.NewErrorBuilder().
					Type(contract.TypeMethodNotAllowed).
					Message("method not allowed").
					Build())
				return
			}
		}
		if fallback := r.state.notFound.Load(); fallback != nil {
			fallback.handler.ServeHTTP(w, req)
		} else {
			http.NotFound(w, req)
		}
		return
	}

	r.attachRouteContextAndServe(w, req, result)
}

// Bound path storage independently of the route count. Large identifiers
// still match normally without displacing frequently accessed results.
const maxCachedDynamicPathBytes = 1024

func (r *Router) cachedMatch(method, path string) *matchResult {
	return r.cachedMatchParts(method, path, nil, false)
}

func (r *Router) cachedMatchParts(method, path string, scratch **[]string, escaped bool) *matchResult {
	// Keep lookup and publication in the same read lock so registration cannot
	// clear the cache between matching a route and publishing the result.
	// Freeze publishes the immutable trie and cache pointer under the write lock.
	decodeSegments := escaped && strings.Contains(path, "%")
	key := matchCacheKey{method: method, path: path, escaped: decodeSegments}
	if r.state.frozen.Load() {
		if result := r.state.frozenStatic[key]; result != nil {
			return result
		}
	} else {
		r.state.mu.RLock()
		defer r.state.mu.RUnlock()
	}

	cache := r.state.matchCache
	if result, ok := cache.Get(key); ok {
		return result
	}
	result := r.matchRouteParts(method, path, scratch, decodeSegments)
	if result != nil && (path == result.RoutePattern || len(path) <= maxCachedDynamicPathBytes) {
		cache.storeMatch(key, result, r.state.frozen.Load() && path != result.RoutePattern)
	}
	return result
}

// matchRoute reads the trie under the caller's read lock or after Freeze.
func (r *Router) matchRoute(method, path string) *matchResult {
	return r.matchRouteParts(method, path, nil, false)
}

func (r *Router) matchRouteParts(method, path string, scratch **[]string, escaped bool) *matchResult {
	if path == "/" {
		if method != MethodAny {
			tree := r.state.trees[method]
			if tree != nil && tree.handler != nil {
				return newMatchResult(tree, "/", method, nil)
			}
		}
		// RFC 7231 §4.3.2: HEAD is identical to GET except no body.
		if method == http.MethodHead {
			if getTree := r.state.trees[http.MethodGet]; getTree != nil && getTree.handler != nil {
				return newMatchResult(getTree, "/", http.MethodGet, nil)
			}
		}
		if anyTree := r.state.trees[MethodAny]; anyTree != nil && anyTree.handler != nil {
			return newMatchResult(anyTree, "/", MethodAny, nil)
		}
		return nil
	}

	partsPtr := splitPathToParts(path)
	parts := *partsPtr
	if scratch != nil && r.state.methodNotAllowed.Load() {
		*scratch = partsPtr
	} else {
		defer putPathParts(partsPtr)
	}

	if r.state.mounts != nil {
		if result := matchNode(r.state.mounts, parts, path, escaped); result != nil {
			result.RouteMethod = MethodAny
			return result
		}
	}

	if method != MethodAny {
		tree := r.state.trees[method]
		if tree != nil {
			result := matchNode(tree, parts, path, escaped)
			if result != nil {
				result.RouteMethod = method
				return result
			}
		}
	}

	// RFC 7231 §4.3.2: fall back to GET handler for HEAD requests.
	if method == http.MethodHead {
		if getTree := r.state.trees[http.MethodGet]; getTree != nil {
			result := matchNode(getTree, parts, path, escaped)
			if result != nil {
				result.RouteMethod = http.MethodGet
				return result
			}
		}
	}

	// Fall back to ANY handler.
	if anyTree := r.state.trees[MethodAny]; anyTree != nil {
		result := matchNode(anyTree, parts, path, escaped)
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
	publishRouteContext(w, ctx, result.RoutePattern, result.RouteName)
	var matchedRequest *http.Request
	if len(result.paramKeys) != 0 && r.state.standardPathValues.Load() {
		matchedRequest = clonePathValues(req, ctx)
		for _, key := range result.paramKeys {
			matchedRequest.SetPathValue(key, contract.RequestParamFromContext(ctx, key))
		}
	} else {
		matchedRequest = req.WithContext(ctx)
	}
	matchedRequest.Pattern = result.RoutePattern
	result.Handler.ServeHTTP(w, matchedRequest)
}

// Clone 会同时复制私有路径参数存储和公开字段。这里保留公开字段的
// 引用关系，尤其是传输层会在请求体读完后更新的 Trailer。
func clonePathValues(req *http.Request, ctx context.Context) *http.Request {
	shallow := *req
	shallow.URL = nil
	shallow.Header, shallow.Trailer = nil, nil
	shallow.TransferEncoding = nil
	shallow.Form, shallow.PostForm, shallow.MultipartForm = nil, nil, nil
	cloned := shallow.Clone(ctx)
	cloned.URL = req.URL
	cloned.Header, cloned.Trailer = req.Header, req.Trailer
	cloned.TransferEncoding = req.TransferEncoding
	cloned.Form, cloned.PostForm, cloned.MultipartForm = req.Form, req.PostForm, req.MultipartForm
	return cloned
}

func (r *Router) allowedMethods(path string, partsPtr *[]string, escaped bool) []string {
	normalized := path
	if normalized == "" {
		normalized = "/"
	}

	if r.state.frozen.Load() {
		return r.frozenAllowedMethods(normalized, partsPtr, escaped)
	}

	r.state.mu.RLock()
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
		if partsPtr == nil {
			partsPtr = splitPathToParts(normalized)
			defer putPathParts(partsPtr)
		}
		parts := *partsPtr

		for method, tree := range r.state.trees {
			if method == MethodAny || tree == nil {
				continue
			}
			if matchBranch(tree, parts, "", 0, nil, escaped) != nil {
				allowed = append(allowed, method)
			}
		}

	}

	r.state.mu.RUnlock()

	allowed = appendImplicitHeadMethod(allowed)
	if len(allowed) > 1 {
		sort.Strings(allowed)
	}
	return allowed
}

func (r *Router) frozenAllowedMethods(path string, parts *[]string, escaped bool) []string {
	if path != "/" && parts == nil {
		parts = splitPathToParts(path)
		defer putPathParts(parts)
	}
	var allowed []string
	var prefixMethods map[string]struct{}
	if parts != nil && len(*parts) > 0 {
		prefix := (*parts)[0]
		if escaped {
			prefix, _ = url.PathUnescape(prefix)
		}
		prefixMethods = r.state.frozenMethodPrefixes[prefix]
	}
	for _, method := range r.state.frozenMethods {
		if path != "/" {
			_, literal := prefixMethods[method]
			_, dynamic := r.state.frozenAnyPrefix[method]
			if !literal && !dynamic {
				continue
			}
		}
		tree := r.state.trees[method]
		if path == "/" && tree.handler != nil || path != "/" && matchBranch(tree, *parts, "", 0, nil, escaped) != nil {
			if allowed == nil {
				allowed = make([]string, 0, len(r.state.frozenMethods)+1)
			}
			allowed = append(allowed, method)
		}
	}
	withHead := appendImplicitHeadMethod(allowed)
	if len(withHead) != len(allowed) {
		index := sort.SearchStrings(allowed, http.MethodHead)
		copy(withHead[index+1:], allowed[index:])
		withHead[index] = http.MethodHead
	}
	return withHead
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
	return append(methods, http.MethodHead)
}

// publishRouteContext notifies optional observers without changing request ownership.
func publishRouteContext(w http.ResponseWriter, ctx context.Context, pattern, name string) {
	for w != nil {
		if receiver, ok := w.(interface {
			SetRouteIdentity(context.Context, string, string)
		}); ok {
			receiver.SetRouteIdentity(ctx, pattern, name)
			return
		}
		if receiver, ok := w.(interface{ SetRouteContext(context.Context) }); ok {
			receiver.SetRouteContext(ctx)
			return
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = wrapper.Unwrap()
	}
}
