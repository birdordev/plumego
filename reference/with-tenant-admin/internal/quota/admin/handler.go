package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	tenantadmin "with-tenant-admin/internal/tenant/admin"
)

type TenantLookup interface {
	Get(context.Context, string) (tenantadmin.TenantRecord, error)
}

// Handler serves the quota admin endpoints.
// Logger must not be nil; pass app.Core.Logger() from app.New.
type Handler struct {
	tenants    TenantLookup
	configs    *tenantcore.InMemoryConfigManager
	quotaStore *tenantcore.InMemoryQuotaStore
	now        func() time.Time
	Logger     plumelog.StructuredLogger
}

type SetQuotaRequest struct {
	Limit int64 `json:"limit"`
}

type QuotaResponse struct {
	TenantID  string `json:"tenant_id"`
	Limit     int64  `json:"limit"`
	Used      int64  `json:"used"`
	Remaining int64  `json:"remaining"`
}

func NewHandler(tenants TenantLookup, configs *tenantcore.InMemoryConfigManager, quotaStore *tenantcore.InMemoryQuotaStore, logger plumelog.StructuredLogger) *Handler {
	if configs == nil {
		configs = tenantcore.NewInMemoryConfigManager()
	}
	if quotaStore == nil {
		quotaStore = tenantcore.NewInMemoryQuotaStore()
	}
	return &Handler{
		tenants:    tenants,
		configs:    configs,
		quotaStore: quotaStore,
		now:        func() time.Time { return time.Now().UTC() },
		Logger:     logger,
	}
}

func (h *Handler) GetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := router.Param(r, "tenantID")
	if err := h.requireTenant(r.Context(), tenantID); err != nil {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}

	response, err := h.quotaResponse(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "get quota failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, response, nil))
}

func (h *Handler) SetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := router.Param(r, "tenantID")
	if err := h.requireTenant(r.Context(), tenantID); err != nil {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}

	var req SetQuotaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, contract.TypeValidation, "invalid request body")
		return
	}
	if req.Limit < 0 {
		h.writeError(w, r, contract.TypeValidation, "quota limit must be non-negative")
		return
	}

	cfg, err := h.tenantConfig(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "set quota failed")
		return
	}
	cfg.Quota = tenantcore.QuotaConfig{
		Limits: []tenantcore.QuotaLimit{{
			Window:   tenantcore.QuotaWindowMinute,
			Requests: req.Limit,
		}},
	}
	h.configs.SetTenantConfig(cfg)

	response, err := h.quotaResponse(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "set quota failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, response, nil))
}

func (h *Handler) ResetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := router.Param(r, "tenantID")
	if err := h.requireTenant(r.Context(), tenantID); err != nil {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}

	windowStart := h.windowStart()
	if usage, ok := h.quotaStore.Usage(tenantID, tenantcore.QuotaWindowMinute, windowStart); ok {
		if err := h.quotaStore.Release(r.Context(), tenantcore.QuotaReleaseRequest{
			TenantID:      tenantID,
			Window:        tenantcore.QuotaWindowMinute,
			WindowStart:   windowStart,
			DeltaRequests: usage.Requests,
			DeltaTokens:   usage.Tokens,
		}); err != nil {
			h.writeError(w, r, contract.TypeInternal, "reset quota failed")
			return
		}
	}

	response, err := h.quotaResponse(r.Context(), tenantID)
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "reset quota failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, response, nil))
}

func (h *Handler) requireTenant(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return tenantadmin.ErrNotFound
	}
	if h.tenants != nil {
		_, err := h.tenants.Get(ctx, tenantID)
		return err
	}
	_, err := h.configs.GetTenantConfig(ctx, tenantID)
	return err
}

func (h *Handler) tenantConfig(ctx context.Context, tenantID string) (tenantcore.Config, error) {
	cfg, err := h.configs.GetTenantConfig(ctx, tenantID)
	if errors.Is(err, tenantcore.ErrTenantNotFound) {
		return tenantcore.Config{TenantID: tenantID}, nil
	}
	return cfg, err
}

func (h *Handler) quotaResponse(ctx context.Context, tenantID string) (QuotaResponse, error) {
	cfg, err := h.tenantConfig(ctx, tenantID)
	if err != nil {
		return QuotaResponse{}, err
	}

	limit := quotaLimit(cfg.Quota)
	used := int64(0)
	if usage, ok := h.quotaStore.Usage(tenantID, tenantcore.QuotaWindowMinute, h.windowStart()); ok {
		used = usage.Requests
	}

	return QuotaResponse{
		TenantID:  tenantID,
		Limit:     limit,
		Used:      used,
		Remaining: remaining(limit, used),
	}, nil
}

func (h *Handler) windowStart() time.Time {
	now := h.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return now().UTC().Truncate(time.Minute)
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

func quotaLimit(cfg tenantcore.QuotaConfig) int64 {
	for _, limit := range cfg.Limits {
		if limit.Window == tenantcore.QuotaWindowMinute {
			return limit.Requests
		}
	}
	return 0
}

func remaining(limit, used int64) int64 {
	if limit <= 0 {
		return -1
	}
	if used >= limit {
		return 0
	}
	return limit - used
}
