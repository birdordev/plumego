package router

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/birdordev/plumego/contract"
)

// MethodAny is the reserved router method sentinel used for fallback routes
// that match any incoming HTTP method.
const MethodAny = "ANY"

// Configuration defaults (unexported; callers configure via RouterOption).
const (
	defaultCacheCapacity = 100
	defaultPoolSliceCap  = 4
	defaultPathPartsCap  = 8
)

// segment represents a path segment with type information.
type segment struct {
	raw       string
	isParam   bool
	isWild    bool
	paramName string
}

// node represents a node in the prefix trie.
type node struct {
	path      string
	paramName string
	fullPath  string
	indices   string
	children  []*node
	handler   http.Handler
	paramKeys []string
	routeName string
}

type route struct {
	Method string
	Path   string
	Meta   RouteMeta
}

// routerState is the shared mutable state for the root router and all groups.
// It excludes application-layer concerns so the router stays a pure
// route-structure primitive.
type routerState struct {
	notFound             atomic.Pointer[notFoundHandler]
	standardPathValues   atomic.Bool
	escapedPathSegments  atomic.Bool
	trees                map[string]*node
	routes               map[string][]route
	mu                   sync.RWMutex
	matchCache           *matchCache
	namedRoutes          map[string]*compiledNamedRoute
	frozenRoutes         []route
	methodNotAllowed     atomic.Bool
	frozenStatic         map[matchCacheKey]*matchResult
	frozenMethods        []string
	frozenMethodPrefixes map[string]map[string]struct{}
	frozenAnyPrefix      map[string]struct{}
	frozen               atomic.Bool
}

// Router represents an HTTP router with path-based routing.
// It implements http.Handler, making it compatible with any Go HTTP server.
//
// Example:
//
//	r := router.NewRouter()
//	if err := r.AddRoute(http.MethodGet, "/users", listUsersHandler); err != nil {
//	    return err
//	}
//	if err := r.AddRoute(http.MethodPost, "/users", createUserHandler); err != nil {
//	    return err
//	}
//
//	api := r.Group("/api/v1")
//	if err := api.AddRoute(http.MethodGet, "/users/:id", getUserHandler); err != nil {
//	    return err
//	}
//
//	http.ListenAndServe(":8080", r)
type Router struct {
	prefix string
	state  *routerState
}

// RouterOption defines a function type for router configuration options.
type RouterOption func(*Router)

// EnableEscapedPathSegments decodes escaped URL segments after splitting the
// path. Call before Freeze to allow escaped slashes inside single parameters.
func (r *Router) EnableEscapedPathSegments() error {
	if !r.ready() {
		return errors.New("router is uninitialized")
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return errors.New("router is frozen")
	}
	r.state.escapedPathSegments.Store(true)
	return nil
}

// EnableStandardPathValues 让路由参数同时可从 http.Request.PathValue 和路由上下文读取。
// 在 Freeze 前调用。此模式为匹配请求复制路径参数存储。
func (r *Router) EnableStandardPathValues() error {
	if !r.ready() {
		return errors.New("router is uninitialized")
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return errors.New("router is frozen")
	}
	r.state.standardPathValues.Store(true)
	return nil
}

type notFoundHandler struct {
	handler http.Handler
}

// WithNotFoundHandler 设置路径或方法未匹配时的处理器。
// 启用方法检查后，自动生成的 405 响应优先。
func WithNotFoundHandler(handler http.Handler) RouterOption {
	return func(r *Router) {
		r.setNotFoundHandler(handler)
	}
}

// SetNotFoundHandler 在 Freeze 前设置未匹配请求的处理器。
// 传入 nil 恢复标准库的 404 响应。
func (r *Router) SetNotFoundHandler(handler http.Handler) error {
	if !r.setNotFoundHandler(handler) {
		return errors.New("router is uninitialized or frozen")
	}
	return nil
}

func (r *Router) setNotFoundHandler(handler http.Handler) bool {
	if !r.ready() {
		return false
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return false
	}
	if handler == nil {
		r.state.notFound.Store(nil)
	} else {
		r.state.notFound.Store(&notFoundHandler{handler: handler})
	}
	return true
}

// RouteOption defines an option for route metadata.
type RouteOption func(*RouteMeta)

// WithRouteName sets a route name for reverse URL generation.
//
// Example:
//
//	r.AddRoute(http.MethodGet, "/users/:id", handler, router.WithRouteName("user.show"))
//	url, err := r.URL("user.show", "id", "123") // → "/users/123"
func WithRouteName(name string) RouteOption {
	return func(meta *RouteMeta) {
		meta.Name = name
	}
}

// WithTags snapshots classification labels for a route. Tags appear in RouteInfo
// snapshots returned by Routes() and can be used by x/* extensions to filter or
// annotate routes (e.g. openapi operation grouping, gateway policy selection).
//
// Example:
//
//	r.AddRoute(http.MethodGet, "/admin/users", handler,
//	    router.WithRouteName("admin.users.list"),
//	    router.WithTags("admin", "users"),
//	)
func WithTags(tags ...string) RouteOption {
	tags = slices.Clone(tags)
	return func(meta *RouteMeta) {
		meta.Tags = append(meta.Tags, tags...)
	}
}

// WithCacheCapacity sets the maximum number of distinct method:path pairs to
// cache per router. The FIFO cache stores only successful route matches and
// evicts its oldest entry when a new match is inserted into a full cache.
// Large dynamic paths bypass admission. Once a frozen cache is full, a new
// dynamic path must be observed again before it can displace a cached match.
// The option has no effect after Freeze.
//
// The default of 100 works well for services whose hot paths are mostly static
// URLs (/healthz, /readyz, /api/info). For fully parameterised APIs (every
// request has a unique URL) the cache provides little benefit regardless of
// size, so leave the default.
func WithCacheCapacity(n int) RouterOption {
	return func(r *Router) {
		if n <= 0 || !r.ready() {
			return
		}
		r.state.mu.Lock()
		defer r.state.mu.Unlock()
		if r.state.frozen.Load() {
			return
		}
		r.state.matchCache = newMatchCache(n)
	}
}

// WithMethodNotAllowed enables returning 405 with Allow header when path matches another method.
func WithMethodNotAllowed(enabled bool) RouterOption {
	return func(r *Router) {
		r.SetMethodNotAllowed(enabled)
	}
}

// NewRouter creates a new Router instance with default configuration.
//
// Example:
//
//	r := router.NewRouter()
//	err := r.AddRoute(http.MethodGet, "/hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    w.Write([]byte("Hello, World!"))
//	}))
func NewRouter(opts ...RouterOption) *Router {
	r := &Router{
		prefix: "",
		state: &routerState{
			trees:       make(map[string]*node),
			routes:      make(map[string][]route),
			namedRoutes: make(map[string]*compiledNamedRoute),
			matchCache:  newMatchCache(defaultCacheCapacity),
		},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}

	return r
}

func (r *Router) ready() bool {
	return r != nil && r.state != nil
}

// SetMethodNotAllowed toggles 405 responses when another method matches the path.
func (r *Router) SetMethodNotAllowed(enabled bool) {
	if !r.ready() {
		return
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return
	}
	r.state.methodNotAllowed.Store(enabled)
}

// MethodNotAllowedEnabled reports whether 405 handling is enabled.
func (r *Router) MethodNotAllowedEnabled() bool {
	if !r.ready() {
		return false
	}
	return r.state.methodNotAllowed.Load()
}

// Freeze prevents the router from accepting new route registrations.
// The write lock serializes publication of routes and the frozen flag,
// making the trie safe to read without a lock.
func (r *Router) Freeze() {
	if !r.ready() {
		return
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return
	}
	r.state.frozenStatic = make(map[matchCacheKey]*matchResult)
	r.state.frozenMethodPrefixes = make(map[string]map[string]struct{})
	r.state.frozenAnyPrefix = make(map[string]struct{})
	for method, routes := range r.state.routes {
		if r.state.methodNotAllowed.Load() && method != MethodAny {
			r.state.frozenMethods = append(r.state.frozenMethods, method)
		}
		for _, route := range routes {
			segments := compilePathSegments(route.Path)
			if r.state.methodNotAllowed.Load() && method != MethodAny && len(segments) != 0 {
				first := segments[0]
				if first.isParam || first.isWild {
					r.state.frozenAnyPrefix[method] = struct{}{}
				} else {
					methods := r.state.frozenMethodPrefixes[first.raw]
					if methods == nil {
						methods = make(map[string]struct{})
						r.state.frozenMethodPrefixes[first.raw] = methods
					}
					methods[method] = struct{}{}
				}
			}
			if slices.ContainsFunc(segments, func(s segment) bool { return s.isParam || s.isWild }) {
				continue
			}
			r.state.frozenStatic[matchCacheKey{method: method, path: route.Path}] = r.matchRoute(method, route.Path)
		}
	}
	sort.Strings(r.state.frozenMethods)
	r.state.frozenRoutes = r.sortedRoutesLocked()
	r.state.frozen.Store(true)
}

func findStaticChild(parent *node, path string) *node {
	if parent == nil || len(parent.children) == 0 {
		return nil
	}

	numChildren := len(parent.children)
	if numChildren <= 2 {
		for i := 0; i < numChildren; i++ {
			if parent.children[i].path == path {
				return parent.children[i]
			}
		}
		return nil
	}

	if len(parent.indices) > 0 && len(path) > 0 {
		firstChar := path[0]
		idx := strings.IndexByte(parent.indices, firstChar)
		if idx == -1 {
			return nil
		}
		for i := idx; i < numChildren && parent.indices[i] == firstChar; i++ {
			if parent.children[i].path == path {
				return parent.children[i]
			}
		}
		return nil
	}

	for i := 0; i < numChildren; i++ {
		if parent.children[i].path == path {
			return parent.children[i]
		}
	}
	return nil
}

func findChildByByte(parent *node, b byte) *node {
	if parent == nil || len(parent.children) == 0 {
		return nil
	}
	if len(parent.indices) > 0 {
		idx := strings.IndexByte(parent.indices, b)
		if idx >= 0 && idx < len(parent.children) {
			return parent.children[idx]
		}
		return nil
	}
	for i := range parent.children {
		if len(parent.children[i].path) > 0 && parent.children[i].path[0] == b {
			return parent.children[i]
		}
	}
	return nil
}

func findParamChild(parent *node) *node { return findChildByByte(parent, ':') }

func findWildChild(parent *node) *node { return findChildByByte(parent, '*') }

// insertChild inserts a child node keeping indices sorted by first byte.
func insertChild(parent *node, child *node) {
	i := 0
	for i < len(parent.indices) && parent.indices[i] <= child.path[0] {
		i++
	}

	parent.indices = parent.indices[:i] + string(child.path[0]) + parent.indices[i:]

	parent.children = append(parent.children, nil)
	copy(parent.children[i+1:], parent.children[i:])
	parent.children[i] = child
}

func (r *Router) fullPath(path string) string {
	fullPath := joinRoutePath(r.prefix, path)
	if fullPath == "" {
		return "/"
	}
	return fullPath
}

func joinRoutePath(prefix, path string) string {
	prefix = strings.TrimRight(prefix, "/")
	if path != "" && path[0] != '/' {
		path = "/" + path
	} else if prefix != "" && strings.HasPrefix(path, "/") {
		path = "/" + strings.TrimLeft(path, "/")
	}
	path = strings.TrimRight(path, "/")

	return canonicalRoutePath(prefix + path)
}

func canonicalRoutePath(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}

	firstNonSlash := 0
	for firstNonSlash < len(path) && path[firstNonSlash] == '/' {
		firstNonSlash++
	}
	if firstNonSlash > 1 {
		path = "/" + path[firstNonSlash:]
	}
	if path == "" {
		return "/"
	}
	return path
}

// Param returns the value of the named path parameter from the request context.
// Returns an empty string if the parameter is not found.
//
// Example:
//
//	r.AddRoute(http.MethodGet, "/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    id := router.Param(r, "id")
//	    fmt.Fprintf(w, "User: %s", id)
//	}))
func Param(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return contract.RequestParamFromContext(r.Context(), name)
}
