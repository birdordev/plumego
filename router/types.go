package router

import (
	"github.com/birdordev/plumego/contract"
	"net/http"
)

// matchResult represents the result of route matching.
type matchResult struct {
	routeState   contract.RouteState // Immutable after cache publication; contexts retain it.
	Handler      http.Handler
	ParamValues  []string
	ParamKeys    []string
	RoutePattern string
	RouteMethod  string
	RouteName    string
}

// RouteMeta describes route metadata attached at registration time.
// All fields have zero values that are safe to omit.
type RouteMeta struct {
	Name string   `json:"name,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

// RouteInfo describes a registered route with metadata.
type RouteInfo struct {
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Meta   RouteMeta `json:"meta,omitempty"`
}

// NamedRoute stores information for reverse URL generation.
type NamedRoute struct {
	Method   string
	Pattern  string
	ParamPos map[string]int // parameter name -> position in pattern
}
