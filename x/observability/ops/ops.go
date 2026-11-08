package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/security/authn"
)

const DefaultBasePath = "/ops"

const (
	codeQueueStatsNotConfigured    = "QUEUE_STATS_NOT_CONFIGURED"
	codeQueueReplayNotConfigured   = "QUEUE_REPLAY_NOT_CONFIGURED"
	codeReceiptLookupNotConfigured = "RECEIPT_LOOKUP_NOT_CONFIGURED"
	codeChannelHealthNotConfigured = "CHANNEL_HEALTH_NOT_CONFIGURED"
	codeTenantQuotaNotConfigured   = "TENANT_QUOTA_NOT_CONFIGURED"
	codeQueueListFailed            = "QUEUE_LIST_FAILED"
	codeQueueStatsFailed           = "QUEUE_STATS_FAILED"
	codeQueueReplayFailed          = "QUEUE_REPLAY_FAILED"
	codeReceiptLookupFailed        = "RECEIPT_LOOKUP_FAILED"
	codeChannelListFailed          = "CHANNEL_LIST_FAILED"
	codeChannelHealthFailed        = "CHANNEL_HEALTH_FAILED"
	codeTenantQuotaFailed          = "TENANT_QUOTA_FAILED"
)

// Handler exposes protected operations endpoints for queue/receipt/tenant management.
type Handler struct {
	cfg    Options
	logger log.StructuredLogger
}

// Options configures the ops component.
type Options struct {
	Enabled  bool
	BasePath string
	Auth     AuthConfig
	Hooks    Hooks
	Logger   log.StructuredLogger
}

// AuthConfig configures authentication for ops endpoints.
// When AllowInsecure is false (default), missing auth configuration will deny all requests.
type AuthConfig struct {
	Token         string
	Middleware    middleware.Middleware
	AllowInsecure bool
}

// Hooks define optional operations hooks.
type Hooks struct {
	QueueStats    func(ctx context.Context, queue string) (QueueStats, error)
	QueueList     func(ctx context.Context) ([]string, error)
	QueueReplay   func(ctx context.Context, req QueueReplayRequest) (QueueReplayResult, error)
	ReceiptLookup func(ctx context.Context, messageID string) (ReceiptRecord, error)
	ChannelHealth func(ctx context.Context, provider string) (ChannelHealth, error)
	ChannelList   func(ctx context.Context) ([]string, error)
	TenantQuota   func(ctx context.Context, tenantID string) (TenantQuotaSnapshot, error)
}

// QueueStats reports current queue counters.
type QueueStats struct {
	Queue     string         `json:"queue"`
	Queued    int64          `json:"queued"`
	Leased    int64          `json:"leased"`
	Dead      int64          `json:"dead"`
	Expired   int64          `json:"expired"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// QueueReplayRequest triggers DLQ/task replay.
type QueueReplayRequest struct {
	Queue  string `json:"queue"`
	Max    int    `json:"max,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// QueueReplayResult reports replay outcome.
type QueueReplayResult struct {
	Queue     string `json:"queue"`
	Requested int    `json:"requested,omitempty"`
	Replayed  int    `json:"replayed"`
	Remaining int    `json:"remaining,omitempty"`
}

// ReceiptRecord describes a delivery receipt record.
type ReceiptRecord struct {
	MessageID   string         `json:"message_id"`
	Status      string         `json:"status"`
	Provider    string         `json:"provider,omitempty"`
	DeliveredAt time.Time      `json:"delivered_at,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// ChannelHealth reports provider status.
type ChannelHealth struct {
	Provider  string         `json:"provider"`
	Status    string         `json:"status"`
	Message   string         `json:"message,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// TenantQuotaSnapshot reports tenant quota usage and limits.
type TenantQuotaSnapshot struct {
	TenantID  string         `json:"tenant_id"`
	Limits    []QuotaLimit   `json:"limits,omitempty"`
	Usage     []QuotaUsage   `json:"usage,omitempty"`
	Frozen    bool           `json:"frozen,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// QuotaLimit is a per-window limit configuration.
type QuotaLimit struct {
	Window   string `json:"window"`
	Requests int    `json:"requests,omitempty"`
	Tokens   int    `json:"tokens,omitempty"`
}

// QuotaUsage reports usage in a quota window.
type QuotaUsage struct {
	Window      string    `json:"window"`
	WindowStart time.Time `json:"window_start,omitempty"`
	WindowEnd   time.Time `json:"window_end,omitempty"`
	Requests    int       `json:"requests,omitempty"`
	Tokens      int       `json:"tokens,omitempty"`
}

type summaryResponse struct {
	BasePath string          `json:"base_path"`
	Auth     summaryAuth     `json:"auth"`
	Features summaryFeatures `json:"features"`
}

type summaryAuth struct {
	Required bool `json:"required"`
	Enabled  bool `json:"enabled"`
}

type summaryFeatures struct {
	QueueStats    bool `json:"queue_stats"`
	QueueReplay   bool `json:"queue_replay"`
	ReceiptLookup bool `json:"receipt_lookup"`
	ChannelHealth bool `json:"channel_health"`
	TenantQuota   bool `json:"tenant_quota"`
}

type queueStatsListResponse struct {
	Queues []QueueStats `json:"queues"`
}

type queueReplayResponse struct {
	Replay QueueReplayResult `json:"replay"`
}

type receiptLookupResponse struct {
	Receipt ReceiptRecord `json:"receipt"`
}

type channelHealthResponse struct {
	Channels []ChannelHealth `json:"channels"`
}

type tenantQuotaResponse struct {
	Quota TenantQuotaSnapshot `json:"quota"`
}

// New constructs an ops handler.
func New(opts Options) *Handler {
	logger := opts.Logger
	if logger == nil {
		logger = log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	}
	return &Handler{
		cfg:    opts,
		logger: logger,
	}
}

// RouteRegistrar is the minimal route-registration interface accepted by
// RegisterRoutes. Both *router.Router and *core.App satisfy it, so callers
// can pass either without exposing the router directly.
type RouteRegistrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

func (c *Handler) RegisterRoutes(r RouteRegistrar) error {
	if !c.cfg.Enabled {
		return nil
	}

	base := normalizeBasePath(c.cfg.BasePath)
	register := func(method, suffix string, handler http.Handler) error {
		return r.AddRoute(method, base+suffix, c.withAuth(handler))
	}

	if err := register(http.MethodGet, "", http.HandlerFunc(c.handleSummary)); err != nil {
		return err
	}
	if err := register(http.MethodGet, "/queue", http.HandlerFunc(c.handleQueueStats)); err != nil {
		return err
	}
	if err := register(http.MethodPost, "/queue/replay", http.HandlerFunc(c.handleQueueReplay)); err != nil {
		return err
	}
	if err := register(http.MethodGet, "/receipts", http.HandlerFunc(c.handleReceiptLookup)); err != nil {
		return err
	}
	if err := register(http.MethodGet, "/channels", http.HandlerFunc(c.handleChannelHealth)); err != nil {
		return err
	}
	return register(http.MethodGet, "/tenants/quota", http.HandlerFunc(c.handleTenantQuota))
}

func (c *Handler) withAuth(handler http.Handler) http.Handler {
	middlewares := c.authMiddlewares()
	if len(middlewares) == 0 {
		return handler
	}
	return middleware.NewChain(middlewares...).Build(handler)
}

func (c *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	data := summaryResponse{
		BasePath: normalizeBasePath(c.cfg.BasePath),
		Auth: summaryAuth{
			Required: !c.cfg.Auth.AllowInsecure,
			Enabled:  c.hasAuthConfigured(),
		},
		Features: summaryFeatures{
			QueueStats:    c.cfg.Hooks.QueueStats != nil,
			QueueReplay:   c.cfg.Hooks.QueueReplay != nil,
			ReceiptLookup: c.cfg.Hooks.ReceiptLookup != nil,
			ChannelHealth: c.cfg.Hooks.ChannelHealth != nil,
			TenantQuota:   c.cfg.Hooks.TenantQuota != nil,
		},
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, data, nil)
}

func (c *Handler) handleQueueStats(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Hooks.QueueStats == nil {
		writeNotImplemented(w, r, codeQueueStatsNotConfigured, "queue stats hook not configured")
		return
	}

	queue := strings.TrimSpace(r.URL.Query().Get("queue"))
	var stats []QueueStats

	if queue == "" {
		if c.cfg.Hooks.QueueList == nil {
			writeRequiredQueryError(w, r, "queue")
			return
		}
		queues, err := c.cfg.Hooks.QueueList(r.Context())
		if err != nil {
			c.writeHookError(w, r, codeQueueListFailed, err)
			return
		}
		stats = make([]QueueStats, 0, len(queues))
		for _, q := range queues {
			snapshot, err := c.cfg.Hooks.QueueStats(r.Context(), q)
			if err != nil {
				c.writeHookError(w, r, codeQueueStatsFailed, err)
				return
			}
			stats = append(stats, snapshot)
		}
	} else {
		snapshot, err := c.cfg.Hooks.QueueStats(r.Context(), queue)
		if err != nil {
			c.writeHookError(w, r, codeQueueStatsFailed, err)
			return
		}
		stats = []QueueStats{snapshot}
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, queueStatsListResponse{Queues: stats}, nil)
}

func (c *Handler) handleQueueReplay(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Hooks.QueueReplay == nil {
		writeNotImplemented(w, r, codeQueueReplayNotConfigured, "queue replay hook not configured")
		return
	}

	var req QueueReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}
	if strings.TrimSpace(req.Queue) == "" {
		writeJSONFieldValidationError(w, r, "queue", "queue is required")
		return
	}
	if req.Max < 1 {
		writeJSONFieldValidationError(w, r, "max", "max must be at least 1")
		return
	}

	result, err := c.cfg.Hooks.QueueReplay(r.Context(), req)
	if err != nil {
		c.writeHookError(w, r, codeQueueReplayFailed, err)
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, queueReplayResponse{Replay: result}, nil)
}

func (c *Handler) handleReceiptLookup(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Hooks.ReceiptLookup == nil {
		writeNotImplemented(w, r, codeReceiptLookupNotConfigured, "receipt lookup hook not configured")
		return
	}

	messageID := strings.TrimSpace(r.URL.Query().Get("message_id"))
	if messageID == "" {
		writeRequiredQueryError(w, r, "message_id")
		return
	}

	receipt, err := c.cfg.Hooks.ReceiptLookup(r.Context(), messageID)
	if err != nil {
		c.writeHookError(w, r, codeReceiptLookupFailed, err)
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, receiptLookupResponse{Receipt: receipt}, nil)
}

func (c *Handler) handleChannelHealth(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Hooks.ChannelHealth == nil {
		writeNotImplemented(w, r, codeChannelHealthNotConfigured, "channel health hook not configured")
		return
	}

	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	var channels []ChannelHealth

	if provider == "" {
		if c.cfg.Hooks.ChannelList == nil {
			writeRequiredQueryError(w, r, "provider")
			return
		}
		list, err := c.cfg.Hooks.ChannelList(r.Context())
		if err != nil {
			c.writeHookError(w, r, codeChannelListFailed, err)
			return
		}
		channels = make([]ChannelHealth, 0, len(list))
		for _, name := range list {
			status, err := c.cfg.Hooks.ChannelHealth(r.Context(), name)
			if err != nil {
				c.writeHookError(w, r, codeChannelHealthFailed, err)
				return
			}
			channels = append(channels, status)
		}
	} else {
		status, err := c.cfg.Hooks.ChannelHealth(r.Context(), provider)
		if err != nil {
			c.writeHookError(w, r, codeChannelHealthFailed, err)
			return
		}
		channels = []ChannelHealth{status}
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, channelHealthResponse{Channels: channels}, nil)
}

func (c *Handler) handleTenantQuota(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Hooks.TenantQuota == nil {
		writeNotImplemented(w, r, codeTenantQuotaNotConfigured, "tenant quota hook not configured")
		return
	}

	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenantID == "" {
		writeRequiredQueryError(w, r, "tenant_id")
		return
	}

	snapshot, err := c.cfg.Hooks.TenantQuota(r.Context(), tenantID)
	if err != nil {
		c.writeHookError(w, r, codeTenantQuotaFailed, err)
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, tenantQuotaResponse{Quota: snapshot}, nil)
}

func (c *Handler) authMiddlewares() []middleware.Middleware {
	var middlewares []middleware.Middleware

	if c.cfg.Auth.Middleware != nil {
		middlewares = append(middlewares, c.cfg.Auth.Middleware)
	}

	token := strings.TrimSpace(c.cfg.Auth.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("AUTH_TOKEN"))
	}
	if token != "" {
		authMw, err := auth.Authenticate(authn.StaticToken(token))
		if err != nil {
			if c.logger != nil {
				c.logger.Warn("ops auth middleware configuration failed", log.Fields{"error": err.Error()})
			}
		} else {
			middlewares = append(middlewares, authMw)
		}
	}

	if !c.cfg.Auth.AllowInsecure && len(middlewares) == 0 {
		middlewares = append(middlewares, denyAllMiddleware())
		if c.logger != nil {
			c.logger.Warn("ops auth not configured; denying all requests", log.Fields{})
		}
	}

	return middlewares
}

func (c *Handler) hasAuthConfigured() bool {
	if c.cfg.Auth.Middleware != nil {
		return true
	}
	if strings.TrimSpace(c.cfg.Auth.Token) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("AUTH_TOKEN")) != "" {
		return true
	}
	return false
}

func (c *Handler) writeHookError(w http.ResponseWriter, r *http.Request, code string, err error) {
	if c.logger != nil && err != nil {
		c.logger.ErrorCtx(r.Context(), "ops hook failed", log.Fields{
			"code":       code,
			"error_type": fmt.Sprintf("%T", err),
			"path":       r.URL.Path,
		})
	}
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Code(code).
		Message("internal error").
		Build())
}

func writeRequiredQueryError(w http.ResponseWriter, r *http.Request, field string) {
	message := field + " parameter required"
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Message("validation failed for field '"+field+"': "+message).
		Detail("field", field).
		Detail("validation_message", message).
		Build())
}

func writeJSONFieldValidationError(w http.ResponseWriter, r *http.Request, field string, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeValidationError).
		Message("validation failed").
		Detail("field", field).
		Detail("validation_message", message).
		Build())
}

func writeNotImplemented(w http.ResponseWriter, r *http.Request, code, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotImplemented).
		Code(code).
		Message(message).
		Build())
}

func denyAllMiddleware() middleware.Middleware {
	return func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeUnauthorized).
				Message("ops auth required").
				Build())
		})
	}
}

func normalizeBasePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultBasePath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}
