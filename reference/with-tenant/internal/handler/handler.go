// Package handler contains HTTP handlers for the with-tenant reference app.
package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
)

// ModelsHandler returns the models available to the resolved tenant.
// The tenant identity is extracted from context — it was placed there by
// the resolve middleware that runs before this handler in the per-route chain.
type ModelsHandler struct {
	Logger plumelog.StructuredLogger
}

// NewModelsHandler constructs a ModelsHandler with the given logger.
func NewModelsHandler(logger plumelog.StructuredLogger) *ModelsHandler {
	return &ModelsHandler{Logger: logger}
}

// ServeHTTP implements http.Handler.
func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantcore.TenantIDFromContext(r.Context())
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, map[string]any{
		"tenant_id": tenantID,
		"models":    []string{"gpt-4o"},
	}, nil))
}
