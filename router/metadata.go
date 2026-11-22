package router

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"sort"
	"strings"
)

func (r *Router) validateRouteMetaLocked(meta RouteMeta) error {
	if meta.Name == "" {
		return nil
	}
	if existing, ok := r.state.namedRoutes[meta.Name]; ok {
		return fmt.Errorf("%w: %q already registered for %s %s", errDuplicateRouteName, meta.Name, existing.Method, existing.Pattern)
	}
	return nil
}

var errDuplicateRouteName = errors.New("duplicate route name")

func (r *Router) registerNamedRoute(name, method, pattern string, paramKeys []string) {
	if r.state.namedRoutes == nil {
		r.state.namedRoutes = make(map[string]*compiledNamedRoute)
	}

	paramPos := make(map[string]int, len(paramKeys))
	for pos, key := range paramKeys {
		paramPos[key] = pos
	}

	compiled := &compiledNamedRoute{NamedRoute: NamedRoute{
		Method:   method,
		Pattern:  pattern,
		ParamPos: paramPos,
	}}
	var static strings.Builder
	for _, part := range compilePathSegments(pattern) {
		if !part.isParam && !part.isWild {
			part.raw = url.PathEscape(part.raw)
		}
		compiled.parts = append(compiled.parts, part)
		static.WriteByte('/')
		static.WriteString(part.raw)
	}
	if len(paramKeys) == 0 {
		compiled.staticURL = static.String()
		if compiled.staticURL == "" {
			compiled.staticURL = "/"
		}
	}
	r.state.namedRoutes[name] = compiled
}

type compiledNamedRoute struct {
	NamedRoute
	parts     []segment
	staticURL string
}

// URL generates a URL for a named route with the given parameters.
// It returns an error when the route is unknown, a required parameter is
// missing or empty, a single-segment parameter contains a slash, a wildcard
// parameter starts or ends with a slash, parameter keys are unpaired, unknown,
// or repeated, or the router is not initialised.
// On error the returned string is always "".
//
// Use [URLMust] at startup time when the route names are compile-time constants
// and a missing route is a programming error.
func (r *Router) URL(name string, params ...string) (string, error) {
	result, reason := r.urlForNamedRoute(name, params...)
	if reason != "" {
		return "", errors.New(reason)
	}
	return result, nil
}

func (r *Router) urlForNamedRoute(name string, params ...string) (string, string) {
	if !r.ready() {
		return "", "router is not initialized"
	}
	r.state.mu.RLock()
	namedRoute, exists := r.state.namedRoutes[name]
	r.state.mu.RUnlock()
	if !exists {
		return "", fmt.Sprintf("named route %q not found", name)
	}
	if len(params)%2 != 0 {
		return "", fmt.Sprintf("named route %q has unpaired URL param key %q", name, params[len(params)-1])
	}

	if len(params) == 0 && namedRoute.staticURL != "" {
		return namedRoute.staticURL, ""
	}
	paramMap := make(map[string]string, len(params)/2)
	for i := 0; i < len(params); i += 2 {
		key := params[i]
		if _, exists := paramMap[key]; exists {
			return "", fmt.Sprintf("named route %q has duplicate URL param key %q", name, key)
		}
		if _, exists := namedRoute.ParamPos[key]; !exists {
			return "", fmt.Sprintf("named route %q has unknown URL param key %q", name, key)
		}
		paramMap[key] = params[i+1]
	}

	var result strings.Builder
	result.Grow(len(namedRoute.Pattern))

	for _, part := range namedRoute.parts {
		result.WriteByte('/')
		if part.isParam {
			paramName := part.paramName
			if val, ok := paramMap[paramName]; ok {
				if val == "" {
					return "", fmt.Sprintf("named route %q has empty required param %q", name, paramName)
				}
				if strings.Contains(val, "/") {
					return "", fmt.Sprintf("named route %q has a slash in single-segment param %q", name, paramName)
				}
				result.WriteString(url.PathEscape(val))
			} else {
				return "", fmt.Sprintf("named route %q missing required param %q", name, paramName)
			}
		} else if part.isWild {
			paramName := part.paramName
			if val, ok := paramMap[paramName]; ok {
				if val == "" {
					return "", fmt.Sprintf("named route %q has empty required param %q", name, paramName)
				}
				if val[0] == '/' || val[len(val)-1] == '/' {
					return "", fmt.Sprintf("named route %q has a leading or trailing slash in wildcard param %q", name, paramName)
				}
				first := true
				for seg := range strings.SplitSeq(val, "/") {
					if !first {
						result.WriteByte('/')
					}
					first = false
					result.WriteString(url.PathEscape(seg))
				}
			} else {
				return "", fmt.Sprintf("named route %q missing required param %q", name, paramName)
			}
		} else {
			result.WriteString(part.raw)
		}
	}

	return result.String(), ""
}

// URLMust generates a URL for a named route and panics if URL generation fails.
func (r *Router) URLMust(name string, params ...string) string {
	result, reason := r.urlForNamedRoute(name, params...)
	if reason != "" {
		panic(reason)
	}
	return result
}

// HasRoute checks if a named route exists.
func (r *Router) HasRoute(name string) bool {
	if !r.ready() {
		return false
	}
	r.state.mu.RLock()
	_, exists := r.state.namedRoutes[name]
	r.state.mu.RUnlock()
	return exists
}

// NamedRoutes returns a copy of all registered named routes.
func (r *Router) NamedRoutes() map[string]*NamedRoute {
	if !r.ready() {
		return map[string]*NamedRoute{}
	}
	r.state.mu.RLock()
	defer r.state.mu.RUnlock()

	result := make(map[string]*NamedRoute, len(r.state.namedRoutes))
	for k, v := range r.state.namedRoutes {
		paramPos := maps.Clone(v.ParamPos)
		if paramPos == nil {
			paramPos = make(map[string]int)
		}
		result[k] = &NamedRoute{
			Method:   v.Method,
			Pattern:  v.Pattern,
			ParamPos: paramPos,
		}
	}
	return result
}

// Routes returns a snapshot of all registered routes with metadata.
func (r *Router) Routes() []RouteInfo {
	if !r.ready() {
		return []RouteInfo{}
	}
	routes := r.printRoutesSnapshot()
	infos := make([]RouteInfo, 0, len(routes))
	for _, entry := range routes {
		info := RouteInfo(entry)
		info.Meta.Tags = append([]string(nil), entry.Meta.Tags...)
		infos = append(infos, info)
	}
	return infos
}

// Print prints all registered routes grouped by method. It stops on the first
// write error.
func (r *Router) Print(w io.Writer) {
	if w == nil {
		return
	}
	if _, err := fmt.Fprintln(w, "Registered Routes:"); err != nil {
		return
	}
	if !r.ready() {
		return
	}

	for _, route := range r.printRoutesSnapshot() {
		label := route.Path
		if strings.Contains(route.Path, "/*") {
			label += "   [wildcard]"
		}
		if _, err := fmt.Fprintf(w, "%-6s %s\n", route.Method, label); err != nil {
			return
		}
	}
}

func (r *Router) printRoutesSnapshot() []route {
	if r.state.frozen.Load() {
		return r.state.frozenRoutes
	}
	r.state.mu.RLock()
	defer r.state.mu.RUnlock()
	return r.sortedRoutesLocked()
}

func (r *Router) sortedRoutesLocked() []route {
	routes := make([]route, 0, r.routeCountLocked())
	for _, byMethod := range r.state.routes {
		routes = append(routes, byMethod...)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Method == routes[j].Method {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}

// routeCountLocked returns the route count while the caller holds the state lock.
func (r *Router) routeCountLocked() int {
	count := 0
	for _, bucket := range r.state.routes {
		count += len(bucket)
	}
	return count
}
