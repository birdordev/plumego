package router

import (
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/spcent/plumego/contract"
)

// noBodyWriter wraps http.ResponseWriter and discards all body writes.
// It is used for matched HEAD requests, including explicit HEAD routes, MethodAny
// fallback, and GET fallback (RFC 7231 §4.3.2).
type noBodyWriter struct {
	http.ResponseWriter
}

func (noBodyWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w noBodyWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(io.Discard, r)
}
func (w noBodyWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

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
	r.attachRouteContextAndServe(w, req, result)
}

func responseWriterForMatch(w http.ResponseWriter, req *http.Request, result *matchResult) http.ResponseWriter {
	if req != nil && req.Method == http.MethodHead && result != nil {
		return noBodyWriter{w}
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

// attachRouteContextAndServe stores route metadata in the request context via
// a pooled RouteState (zero map allocations for params), calls the handler,
// then returns the RouteState to the pool.
//
// Callers must not retain the RouteState pointer beyond the handler's return.
// Goroutines that need route params should capture them via
// contract.RequestContextFromContext before launch.
func (r *Router) attachRouteContextAndServe(w http.ResponseWriter, req *http.Request, result *matchResult) {
	if result == nil {
		return
	}

	rs := contract.GetRouteState()
	rs.SetPattern(result.RoutePattern)
	rs.SetName(result.RouteName)
	for i, k := range result.ParamKeys {
		if i >= len(result.ParamValues) {
			break
		}
		rs.AddParam(k, result.ParamValues[i])
	}

	ctx := contract.WithRouteState(req.Context(), rs)
	result.Handler.ServeHTTP(w, req.WithContext(ctx))

	rs.Reset()
	contract.PutRouteState(rs)
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
