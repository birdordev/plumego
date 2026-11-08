package router

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Group creates a new router group with the given prefix.
// Groups allow sharing a common path prefix across multiple routes.
//
// Group panics when prefix is a programmer error (does not begin with "/").
// This mirrors the behaviour of [regexp.MustCompile]: Group is intended for
// startup wiring where the prefix is a compile-time constant; a bad value is
// always a bug, not a runtime condition. All dynamic errors — duplicate routes,
// frozen router — are still returned as errors from [AddRoute].
func (r *Router) Group(prefix string) *Router {
	mustValidateGroupPrefix(prefix)
	if !r.ready() {
		return &Router{
			prefix: normalizeGroupPrefix("", prefix),
		}
	}
	fullPrefix := normalizeGroupPrefix(r.prefix, prefix)

	return &Router{
		prefix: fullPrefix,
		state:  r.state,
	}
}

func normalizeGroupPrefix(parent, child string) string {
	combined := joinRoutePath(parent, child)
	if combined == "/" {
		return ""
	}
	return combined
}

func mustValidateGroupPrefix(prefix string) {
	if err := validateRoutePath(prefix); err != nil {
		panic(fmt.Sprintf("router group %s: %v", prefix, err))
	}
}

func routeRegistrationNotInitializedError(method, path string) error {
	return fmt.Errorf("router add_route %s %s: router is not initialized", method, path)
}

func routeRegistrationFrozenError(method, path string) error {
	return fmt.Errorf("router add_route %s %s: router is frozen", method, path)
}

func (r *Router) routeRegistrationReadinessError(method, path string) error {
	if !r.ready() {
		return routeRegistrationNotInitializedError(method, path)
	}
	return nil
}

func (r *Router) routeRegistrationLifecycleError(method, path string) error {
	if err := r.routeRegistrationReadinessError(method, path); err != nil {
		return err
	}
	if r.state.frozen.Load() {
		return routeRegistrationFrozenError(method, path)
	}
	return nil
}

// AddRoute adds a route to the router with the given method, path, handler, and
// optional route metadata.
func (r *Router) AddRoute(method, path string, handler http.Handler, opts ...RouteOption) error {
	if err := r.routeRegistrationReadinessError(method, path); err != nil {
		return err
	}

	r.state.mu.Lock()
	defer r.state.mu.Unlock()

	if r.state.frozen.Load() {
		return routeRegistrationFrozenError(method, path)
	}
	if err := validateMethod(method); err != nil {
		return fmt.Errorf("router add_route %s %s: %w", method, path, err)
	}
	if handler == nil {
		return fmt.Errorf("router add_route %s %s: nil handler", method, path)
	}
	if err := validateRoutePath(path); err != nil {
		return fmt.Errorf("router add_route %s %s: %w", method, path, err)
	}
	meta := routeMetaFromOptions(opts...)
	meta.Tags = append([]string(nil), meta.Tags...)

	fullPath := r.fullPath(path)
	if err := r.validateRouteMetaLocked(meta); err != nil {
		return fmt.Errorf("router add_route %s %s: %w", method, fullPath, err)
	}

	if r.state.trees[method] == nil {
		r.state.trees[method] = &node{}
	}

	current := r.state.trees[method]
	if fullPath == "/" {
		return r.finalizeRouteRegistrationLocked(method, fullPath, current, handler, nil, meta)
	}

	segments := compilePathSegments(fullPath)
	if err := validateRouteSegments(fullPath, segments); err != nil {
		return fmt.Errorf("router add_route %s %s: %w", method, fullPath, err)
	}
	paramKeys := make([]string, 0, len(segments))

	for _, seg := range segments {
		if seg.isParam {
			paramKeys = append(paramKeys, seg.paramName)
			child := findParamChild(current)
			if child != nil {
				if child.paramName == "" {
					child.paramName = seg.paramName
				} else if child.paramName != seg.paramName {
					return fmt.Errorf("router add_route %s %s: route conflict: parameter name mismatch. existing: %s, new: %s", method, fullPath, child.paramName, seg.paramName)
				}
				current = child
				continue
			}

			child = &node{path: ":", paramName: seg.paramName}
			insertChild(current, child)
			current = child
			continue
		}

		if seg.isWild {
			paramKeys = append(paramKeys, seg.paramName)
			child := findWildChild(current)
			if child != nil {
				if child.paramName == "" {
					child.paramName = seg.paramName
				} else if child.paramName != seg.paramName {
					return fmt.Errorf("router add_route %s %s: route conflict: wildcard parameter name mismatch. existing: %s, new: %s", method, fullPath, child.paramName, seg.paramName)
				}
				current = child
				continue
			}

			child = &node{path: "*", paramName: seg.paramName}
			insertChild(current, child)
			current = child
			continue
		}

		child := findStaticChild(current, seg.raw)
		if child == nil {
			child = &node{path: seg.raw}
			insertChild(current, child)
		}

		current = child
	}

	return r.finalizeRouteRegistrationLocked(method, fullPath, current, handler, paramKeys, meta)
}

func (r *Router) finalizeRouteRegistrationLocked(
	method, fullPath string,
	current *node,
	handler http.Handler,
	paramKeys []string,
	meta RouteMeta,
) error {
	if current.handler != nil {
		return fmt.Errorf("router add_route %s %s: duplicate route registration", method, fullPath)
	}
	current.handler = handler
	current.paramKeys = paramKeys
	current.fullPath = fullPath
	current.routeName = meta.Name

	if meta.Name != "" {
		r.registerNamedRoute(meta.Name, method, fullPath)
	}
	r.state.routes[method] = append(r.state.routes[method], route{Method: method, Path: fullPath, Meta: meta})
	r.clearMatchCacheLocked()
	return nil
}

func (r *Router) clearMatchCacheLocked() {
	if r.state.matchCache != nil {
		r.state.matchCache.Clear()
	}
}

func routeMetaFromOptions(opts ...RouteOption) RouteMeta {
	meta := RouteMeta{}
	if len(opts) == 0 {
		return meta
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&meta)
		}
	}
	return meta
}

func validateRoutePath(path string) error {
	if path != "" && path[0] != '/' {
		return fmt.Errorf("route path %q must start with /", path)
	}
	return nil
}

func validateMethod(method string) error {
	if method == "" {
		return errors.New("empty method")
	}
	if strings.TrimSpace(method) != method {
		return fmt.Errorf("method %q has surrounding whitespace", method)
	}
	for i := 0; i < len(method); i++ {
		if method[i] == ' ' || method[i] == '\t' || method[i] == '\n' || method[i] == '\r' {
			return fmt.Errorf("method %q contains whitespace", method)
		}
		if !isHTTPTokenChar(method[i]) {
			return fmt.Errorf("method %q contains invalid token character %q", method, method[i])
		}
	}
	return nil
}

func isHTTPTokenChar(b byte) bool {
	switch {
	case '0' <= b && b <= '9':
		return true
	case 'A' <= b && b <= 'Z':
		return true
	case 'a' <= b && b <= 'z':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// compilePathSegments parses a URL path into route segments.
func compilePathSegments(path string) []segment {
	if path == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	segments := make([]segment, 0, len(parts))
	for _, segmentPart := range parts {
		if strings.HasPrefix(segmentPart, ":") {
			segments = append(segments, segment{
				raw:       segmentPart,
				isParam:   true,
				paramName: segmentPart[1:],
			})
		} else if strings.HasPrefix(segmentPart, "*") {
			segments = append(segments, segment{
				raw:       segmentPart,
				isWild:    true,
				paramName: segmentPart[1:],
			})
		} else {
			segments = append(segments, segment{raw: segmentPart})
		}
	}
	return segments
}

func validateRouteSegments(path string, segments []segment) error {
	seenParams := make(map[string]struct{})
	for i, seg := range segments {
		switch {
		case seg.raw == "":
			return fmt.Errorf("route pattern %s has empty path segment", path)
		case seg.isParam && seg.paramName == "":
			return fmt.Errorf("route pattern %s has empty parameter name", path)
		case seg.isParam && !isRouteParamName(seg.paramName):
			return fmt.Errorf("route pattern %s has invalid parameter name %q", path, seg.paramName)
		case seg.isWild && seg.paramName == "":
			return fmt.Errorf("route pattern %s has empty wildcard name", path)
		case seg.isWild && !isRouteParamName(seg.paramName):
			return fmt.Errorf("route pattern %s has invalid wildcard name %q", path, seg.paramName)
		case seg.isWild && i != len(segments)-1:
			return fmt.Errorf("route pattern %s has non-terminal wildcard segment", path)
		}
		if seg.isParam || seg.isWild {
			if _, exists := seenParams[seg.paramName]; exists {
				return fmt.Errorf("route pattern %s has duplicate parameter name %q", path, seg.paramName)
			}
			seenParams[seg.paramName] = struct{}{}
		}
	}
	return nil
}

func isRouteParamName(name string) bool {
	if name == "" {
		return false
	}
	if !isParamNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isParamNameChar(name[i]) {
			return false
		}
	}
	return true
}

func isParamNameStart(b byte) bool {
	return b == '_' || ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

func isParamNameChar(b byte) bool {
	return isParamNameStart(b) || ('0' <= b && b <= '9')
}
