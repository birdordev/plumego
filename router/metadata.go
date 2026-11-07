package router

import (
	"errors"
	"fmt"
	"io"
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

func (r *Router) registerNamedRoute(name, method, pattern string) {
	if r.state.namedRoutes == nil {
		r.state.namedRoutes = make(map[string]*NamedRoute)
	}

	paramPos := make(map[string]int)
	parts := strings.Split(strings.Trim(pattern, "/"), "/")
	pos := 0
	for _, part := range parts {
		if strings.HasPrefix(part, ":") {
			paramPos[part[1:]] = pos
			pos++
		} else if strings.HasPrefix(part, "*") {
			paramPos[part[1:]] = pos
			pos++
		}
	}

	r.state.namedRoutes[name] = &NamedRoute{
		Method:   method,
		Pattern:  pattern,
		ParamPos: paramPos,
	}
}

// URL generates a URL for a named route with the given parameters.
// It returns an error when the route is unknown, a required parameter is
// missing or empty, parameters are unpaired, or the router is not initialised.
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

	result := namedRoute.Pattern
	parts := strings.Split(strings.Trim(result, "/"), "/")
	resultParts := make([]string, 0, len(parts))

	for _, part := range parts {
		if strings.HasPrefix(part, ":") {
			paramName := part[1:]
			if val, ok := paramMap[paramName]; ok {
				if val == "" {
					return "", fmt.Sprintf("named route %q has empty required param %q", name, paramName)
				}
				resultParts = append(resultParts, url.PathEscape(val))
			} else {
				return "", fmt.Sprintf("named route %q missing required param %q", name, paramName)
			}
		} else if strings.HasPrefix(part, "*") {
			paramName := part[1:]
			if val, ok := paramMap[paramName]; ok {
				if val == "" {
					return "", fmt.Sprintf("named route %q has empty required param %q", name, paramName)
				}
				segments := strings.Split(val, "/")
				for i, seg := range segments {
					segments[i] = url.PathEscape(seg)
				}
				resultParts = append(resultParts, strings.Join(segments, "/"))
			} else {
				return "", fmt.Sprintf("named route %q missing required param %q", name, paramName)
			}
		} else {
			resultParts = append(resultParts, part)
		}
	}

	return "/" + strings.Join(resultParts, "/"), ""
}

// URLMust generates a URL for a named route and panics if the route doesn't exist.
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
		paramPos := make(map[string]int, len(v.ParamPos))
		for pk, pv := range v.ParamPos {
			paramPos[pk] = pv
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
	r.state.mu.RLock()
	defer r.state.mu.RUnlock()

	infos := make([]RouteInfo, 0)
	for _, routes := range r.state.routes {
		for _, entry := range routes {
			infos = append(infos, RouteInfo(entry))
		}
	}

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Method == infos[j].Method {
			return infos[i].Path < infos[j].Path
		}
		return infos[i].Method < infos[j].Method
	})

	return infos
}

// Print prints all registered routes grouped by method.
func (r *Router) Print(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, "Registered Routes:")
	if !r.ready() {
		return
	}

	for _, route := range r.printRoutesSnapshot() {
		label := route.Path
		if strings.Contains(route.Path, "/*") {
			label += "   [wildcard]"
		}
		_, _ = fmt.Fprintf(w, "%-6s %s\n", route.Method, label)
	}
}

func (r *Router) printRoutesSnapshot() []route {
	r.state.mu.RLock()
	routes := make([]route, 0)
	for _, byMethod := range r.state.routes {
		routes = append(routes, byMethod...)
	}
	r.state.mu.RUnlock()

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Method == routes[j].Method {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}
