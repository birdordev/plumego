package usage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	tenantadmin "with-tenant-admin/internal/tenant/admin"
)

type TenantLookup interface {
	Get(context.Context, string) (tenantadmin.TenantRecord, error)
}

type Store interface {
	Record(context.Context, string, string, int64) error
	Report(context.Context, string) ([]UsageRecord, error)
}

// Handler serves the usage admin endpoints.
// Logger must not be nil; pass app.Core.Logger() from app.New.
type Handler struct {
	tenants TenantLookup
	store   Store
	Logger  plumelog.StructuredLogger
}

type RecordUsageRequest struct {
	Resource string `json:"resource"`
	Count    int64  `json:"count"`
}

func NewHandler(tenants TenantLookup, store Store, logger plumelog.StructuredLogger) *Handler {
	if store == nil {
		store = NewInMemoryUsageStore()
	}
	return &Handler{tenants: tenants, store: store, Logger: logger}
}

func (h *Handler) RecordUsage(w http.ResponseWriter, r *http.Request) {
	tenantID := router.Param(r, "tenantID")
	if err := h.requireTenant(r.Context(), tenantID); err != nil {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}

	var req RecordUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, contract.TypeValidation, "invalid request body")
		return
	}
	resource := strings.TrimSpace(req.Resource)
	if resource == "" {
		h.writeError(w, r, contract.TypeValidation, "usage resource is required")
		return
	}
	if req.Count <= 0 {
		h.writeError(w, r, contract.TypeValidation, "usage count must be positive")
		return
	}

	if err := h.store.Record(r.Context(), tenantID, resource, req.Count); err != nil {
		h.writeError(w, r, contract.TypeInternal, "record usage failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusAccepted, nil, nil))
}

func (h *Handler) GetUsageReport(w http.ResponseWriter, r *http.Request) {
	tenantID := router.Param(r, "tenantID")
	if err := h.requireTenant(r.Context(), tenantID); err != nil {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}

	records, err := h.store.Report(r.Context(), tenantID)
	if errors.Is(err, ErrNotFound) {
		records = []UsageRecord{}
	} else if err != nil {
		h.writeError(w, r, contract.TypeInternal, "get usage report failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, records, nil))
}

func (h *Handler) requireTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return tenantadmin.ErrNotFound
	}
	if h.tenants == nil {
		return nil
	}
	_, err := h.tenants.Get(ctx, tenantID)
	return err
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, typ contract.ErrorType, message string) {
	logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(typ).
		Message(message).
		Build()))
}

func logWriteErr(logger plumelog.StructuredLogger, err error) {
	if err == nil || logger == nil {
		return
	}
	logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
}
