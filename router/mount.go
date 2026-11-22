package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
)

// Mount delegates a static path prefix and its descendants to handler in one
// registration. The most specific mounted prefix wins before ordinary routes.
// The handler receives the original URL, including the prefix.
func (r *Router) Mount(prefix string, handler http.Handler) error {
	if err := r.routeRegistrationLifecycleError(MethodAny, prefix); err != nil {
		return err
	}
	fullPath := r.fullPath(prefix)
	if prefix == "" || prefix[0] != '/' || fullPath == "/" || strings.ContainsAny(fullPath, ":*") {
		return fmt.Errorf("router mount %q: expected a non-root static prefix", prefix)
	}
	segments := compilePathSegments(fullPath)
	if err := validateRouteSegments(fullPath, segments); err != nil {
		return err
	}
	handlerFunc, isHandlerFunc := handler.(http.HandlerFunc)
	if handler == nil || isHandlerFunc && handlerFunc == nil {
		return fmt.Errorf("router mount %q: %w", fullPath, contract.ErrHandlerNil)
	}

	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if r.state.frozen.Load() {
		return routeRegistrationFrozenError(MethodAny, prefix)
	}
	if alreadyMounted := mountRouteNode(r.state.mounts, segments); alreadyMounted != nil && alreadyMounted.handler != nil {
		return fmt.Errorf("router mount %q: duplicate mount", fullPath)
	}
	if existing := mountRouteNode(r.state.trees[MethodAny], segments); existing != nil {
		wild := findWildChild(existing)
		if existing.handler != nil || wild != nil && wild.handler != nil {
			return fmt.Errorf("router mount %q: conflicts with an existing ANY registration", fullPath)
		}
	}

	if r.state.mounts == nil {
		r.state.mounts = &node{}
	}
	current := r.state.mounts
	for _, part := range segments {
		child := findStaticChild(current, part.raw)
		if child == nil {
			child = &node{path: part.raw}
			insertChild(current, child)
		}
		current = child
	}

	const param = "mountPath"
	wildcard := fullPath + "/*" + param
	current.fullPath, current.handler = fullPath, handler
	insertChild(current, &node{
		path: "*", fullPath: wildcard, handler: handler, paramName: param, paramKeys: []string{param},
	})
	r.state.routes[MethodAny] = append(r.state.routes[MethodAny],
		route{Method: MethodAny, Path: fullPath}, route{Method: MethodAny, Path: wildcard},
	)
	r.clearMatchCacheLocked()
	return nil
}

// mountRouteNode looks up a literal route or a terminal wildcard by shape.
func mountRouteNode(root *node, segments []segment) *node {
	for _, part := range segments {
		if root == nil || part.isParam {
			return nil
		}
		if part.isWild {
			root = findWildChild(root)
		} else {
			root = findStaticChild(root, part.raw)
		}
	}
	return root
}
