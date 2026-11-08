// Package handler contains the HTTP handlers for the reference application.
package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

const codeGreetNameRequired = "greet.name.required"

// APIHandler handles the core JSON API endpoints.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
// Use plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard}) in tests.
// ServiceName carries the service identity string; set it from config so every
// response reflects the actual service name rather than a hardcoded placeholder.
// Version carries the build-time version string injected via main.go ldflags.
type APIHandler struct {
	Logger      plumelog.StructuredLogger
	ServiceName string
	Version     string
}

type rootResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Docs    string `json:"docs"`
}

// endpointInfo describes a single HTTP endpoint in the service discovery response.
type endpointInfo struct {
	Name        string `json:"name"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type helloResponse struct {
	Message   string         `json:"message"`
	Service   string         `json:"service"`
	Timestamp string         `json:"timestamp"`
	Version   string         `json:"version"`
	Endpoints []endpointInfo `json:"endpoints"`
}

type greetResponse struct {
	Message string `json:"message"`
}

type infoResponse struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
}

// Root responds with minimal service identity. For the full endpoint listing use GET /api/hello.
func (h APIHandler) Root(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, rootResponse{
		Service: h.ServiceName,
		Version: h.Version,
		Docs:    "/api/hello",
	}, nil))
}

// Hello responds with service metadata and the canonical endpoint list.
//
// The list is hand-authored rather than derived from a.Core.Routes() by design:
// router.RouteInfo exposes only Method, Path, and Meta.Name — it carries no
// human-readable Description, which is editorial metadata the router cannot
// supply. Auto-generating the list would therefore drop the descriptions that
// make this a useful service-discovery endpoint. The tradeoff is that adding a
// route in routes.go requires adding a matching entry here; app_test.go
// TestHelloEndpointListMatchesRegisteredRoutes fails loudly if the two drift,
// so the duplication cannot go stale silently.
//
// Ordered by method (DELETE < GET < POST < PUT) then path — matching
// the sort order returned by a.Core.Routes().
func (h APIHandler) Hello(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, helloResponse{
		Message:   "hello from plumego standard-service",
		Service:   h.ServiceName,
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   h.Version,
		Endpoints: []endpointInfo{
			{Name: "items_delete", Method: http.MethodDelete, Path: "/api/v1/items/:id", Description: "delete an item"},
			{Name: "root", Method: http.MethodGet, Path: "/", Description: "service identity"},
			{Name: "api_hello", Method: http.MethodGet, Path: "/api/hello", Description: "service discovery — this endpoint"},
			{Name: "api_info", Method: http.MethodGet, Path: "/api/info", Description: "application wiring and module info"},
			{Name: "api_greet", Method: http.MethodGet, Path: "/api/v1/greet", Description: "greeting (demonstrates TypeRequired error)"},
			{Name: "items_list", Method: http.MethodGet, Path: "/api/v1/items", Description: "list items with limit/offset pagination"},
			{Name: "items_get", Method: http.MethodGet, Path: "/api/v1/items/:id", Description: "get item by id"},
			{Name: "healthz", Method: http.MethodGet, Path: "/healthz", Description: "liveness probe"},
			{Name: "readyz", Method: http.MethodGet, Path: "/readyz", Description: "readiness probe"},
			{Name: "items_patch", Method: http.MethodPatch, Path: "/api/v1/items/:id", Description: "partially update an item (non-empty fields only)"},
			{Name: "items_create", Method: http.MethodPost, Path: "/api/v1/items", Description: "create an item"},
			{Name: "items_update", Method: http.MethodPut, Path: "/api/v1/items/:id", Description: "update an item (replaces all fields)"},
		},
	}, nil))
}

// Greet demonstrates the canonical error response pattern.
// It requires the "name" query parameter and returns a structured
// TypeRequired error if it is absent.
//
// Example:
//
//	GET /api/v1/greet?name=Alice  → 200 {"message":"hello, Alice"}
//	GET /api/v1/greet             → 400 structured APIError
func (h APIHandler) Greet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Code(codeGreetNameRequired).
			Detail("field", "name").
			Message("name is required").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, greetResponse{Message: "hello, " + name}, nil))
}

// Info responds with application identity information.
// It demonstrates two structured-logging patterns:
//   - WithFields attaches fixed fields to every subsequent log call.
//   - contract.RequestIDFromContext extracts the correlation ID stamped by
//     the requestid middleware so log lines are linkable to the inbound request.
//
// This is an informational endpoint about the running service, not a health
// probe. Use /healthz for liveness and /readyz for readiness.
func (h APIHandler) Info(w http.ResponseWriter, r *http.Request) {
	requestID := contract.RequestIDFromContext(r.Context())
	h.Logger.WithFields(plumelog.Fields{
		"endpoint":   "info",
		"request_id": requestID,
	}).Info("info request handled")
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, infoResponse{
		Service:   h.ServiceName,
		Version:   h.Version,
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil))
}
