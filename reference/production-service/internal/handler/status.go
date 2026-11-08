package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

// StatusConfig carries all observable state needed to build the /api/status response.
// Construct it once at startup from app and config values and pass it to StatusHandler.
// This avoids coupling the handler to the full App or Config struct.
type StatusConfig struct {
	ServiceName    string
	Environment    string
	BodyLimitBytes int64
	RequestTimeout time.Duration
	RateLimit      float64
	RateBurst      int
	Profiles       ProfilesSummary
}

// ProfilesSummary carries the observable storage policy for the /api/status response.
type ProfilesSummary struct {
	Kind        string
	Replacement string
	Path        string
}

// StatusHandler serves GET /api/status.
// Logger must not be nil; pass a.Core.Logger() from routes.go.
type StatusHandler struct {
	Cfg    StatusConfig
	Logger plumelog.StructuredLogger
}

// StatusResponse is the /api/status response body.
// Exported so integration tests can decode it without re-declaring the struct.
type StatusResponse struct {
	Status     string                 `json:"status"`
	Service    string                 `json:"service"`
	Timestamp  string                 `json:"timestamp"`
	Deployment StatusDeploymentPolicy `json:"deployment"`
	Limits     StatusLimits           `json:"limits"`
	Security   StatusSecurityPolicy   `json:"security"`
	Storage    StatusStoragePolicy    `json:"storage"`
	API        StatusAPIPolicy        `json:"api"`
	Ops        StatusOpsPolicy        `json:"ops"`
}

// StatusDeploymentPolicy describes the deployment configuration in /api/status.
type StatusDeploymentPolicy struct {
	Environment string `json:"environment"`
	Config      string `json:"config"`
	Secrets     string `json:"secrets"`
}

// StatusLimits describes the rate and size limits in /api/status.
type StatusLimits struct {
	BodyLimitBytes int64   `json:"body_limit_bytes"`
	RequestTimeout string  `json:"request_timeout"`
	RateLimit      float64 `json:"rate_limit"`
	RateBurst      int     `json:"rate_burst"`
}

// StatusSecurityPolicy describes the auth requirements in /api/status.
type StatusSecurityPolicy struct {
	APIToken string `json:"api_token"`
	OpsToken string `json:"ops_token"`
}

// StatusStoragePolicy describes the profile store in /api/status.
type StatusStoragePolicy struct {
	ProfileStore string `json:"profile_store"`
	Replacement  string `json:"replacement"`
	Path         string `json:"path,omitempty"`
}

// StatusAPIPolicy describes the protected API surface in /api/status.
type StatusAPIPolicy struct {
	ProfileRoute string `json:"profile_route"`
	Auth         string `json:"auth"`
	Tenant       string `json:"tenant"`
	Storage      string `json:"storage"`
}

// StatusOpsPolicy describes the ops surface in /api/status.
type StatusOpsPolicy struct {
	HealthRoutes string `json:"health_routes"`
	MetricsRoute string `json:"metrics_route"`
	AdminRoutes  string `json:"admin_routes"`
	OpsAuth      string `json:"ops_auth"`
	Devtools     string `json:"devtools"`
}

// Status responds with a full production configuration summary.
// It surfaces implemented behavior only — no token values are included.
func (h StatusHandler) Status(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, StatusResponse{
		Status:    "healthy",
		Service:   h.Cfg.ServiceName,
		Timestamp: utcNow(),
		Deployment: StatusDeploymentPolicy{
			Environment: h.Cfg.Environment,
			Config:      "environment_and_flags",
			Secrets:     "environment_only_no_fallback_for_tokens",
		},
		Limits: StatusLimits{
			BodyLimitBytes: h.Cfg.BodyLimitBytes,
			RequestTimeout: h.Cfg.RequestTimeout.String(),
			RateLimit:      h.Cfg.RateLimit,
			RateBurst:      h.Cfg.RateBurst,
		},
		Security: StatusSecurityPolicy{
			APIToken: "APP_API_TOKEN required for /api/profile",
			OpsToken: "OPS_TOKEN required for /ops/metrics",
		},
		Storage: StatusStoragePolicy{
			ProfileStore: h.Cfg.Profiles.Kind,
			Replacement:  h.Cfg.Profiles.Replacement,
			Path:         h.Cfg.Profiles.Path,
		},
		API: StatusAPIPolicy{
			ProfileRoute: "/api/profile",
			Auth:         "bearer_token_required",
			Tenant:       "X-Tenant-ID required",
			Storage:      "see storage.profile_store",
		},
		Ops: StatusOpsPolicy{
			HealthRoutes: "/healthz and /readyz are public by default",
			MetricsRoute: "/ops/metrics",
			AdminRoutes:  "not_mounted_by_default",
			OpsAuth:      "bearer_token_required",
			Devtools:     "not_mounted_by_default",
		},
	}, nil))
}
