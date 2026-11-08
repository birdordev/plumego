package handler

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
	"production-service/internal/domain/tenant"
)

// ProfileStore is the minimal persistence interface that ProfileHandler depends on.
// All methods accept a context so callers can propagate request deadlines and
// cancellation to real storage backends. Pass a concrete implementation from
// routes.go; pass a stub in tests.
type ProfileStore interface {
	Get(ctx context.Context, tenantID string) (tenant.Profile, bool)
}

// ProfileHandler serves GET /api/profile.
// It reads the tenant ID from request context (populated by x/tenant/resolve middleware)
// and returns the matching profile or a 404 TypeNotFound error.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
type ProfileHandler struct {
	Profiles ProfileStore
	Logger   plumelog.StructuredLogger
}

const codeProfileNotFound = "profile.not_found"

// GetProfile handles GET /api/profile.
//
//	GET /api/profile (with X-Tenant-ID: tenant-a) → 200 tenant.Profile
//	GET /api/profile (unknown tenant)              → 404 TypeNotFound
func (h ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantcore.TenantIDFromContext(r.Context())
	profile, ok := h.Profiles.Get(r.Context(), tenantID)
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(codeProfileNotFound).
			Detail("tenant_id", tenantID).
			Message("tenant profile not found").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, profile, nil))
}
