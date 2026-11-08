package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/internal/stringsx"
)

type Outbound struct {
	cfg WebhookOutConfig
}

func NewOutbound(cfg WebhookOutConfig) *Outbound {
	return &Outbound{cfg: cfg}
}

func (c *Outbound) RegisterRoutes(r routeRegistrar) error {
	if !c.cfg.Enabled || c.cfg.Service == nil {
		return nil
	}

	base := strings.TrimSpace(c.cfg.BasePath)
	if base == "" {
		base = "/webhooks"
	}

	svc := c.cfg.Service

	if err := r.AddRoute(http.MethodPost, base+"/targets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookCreateTarget(w, r, svc) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, base+"/targets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookListTargets(w, r, svc) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, base+"/targets/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookGetTarget(w, r, svc) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPatch, base+"/targets/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookPatchTarget(w, r, svc) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPost, base+"/targets/:id/enable", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookSetTargetEnabled(w, r, svc, true) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPost, base+"/targets/:id/disable", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookSetTargetEnabled(w, r, svc, false) })); err != nil {
		return err
	}
	token, allowEmpty := c.cfg.TriggerToken, c.cfg.AllowEmptyToken
	if err := r.AddRoute(http.MethodPost, base+"/events/:event", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookTriggerEvent(w, r, svc, token, allowEmpty)
	})); err != nil {
		return err
	}
	pageLimit := c.cfg.DefaultPageLimit
	if err := r.AddRoute(http.MethodGet, base+"/deliveries", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookListDeliveries(w, r, svc, pageLimit) })); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, base+"/deliveries/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookGetDelivery(w, r, svc) })); err != nil {
		return err
	}
	return r.AddRoute(http.MethodPost, base+"/deliveries/:id/replay", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { webhookReplayDelivery(w, r, svc) }))
}

func (c *Outbound) Health() (string, health.HealthStatus) {
	status := health.HealthStatus{Status: health.StatusHealthy, Details: map[string]any{"enabled": c.cfg.Enabled}}

	if !c.cfg.Enabled {
		status.Status = health.StatusDegraded
		status.Message = "component disabled"
	}

	return "webhook_out", status
}

type targetDTO struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`

	Headers map[string]string `json:"headers,omitempty"`

	TimeoutMs     int   `json:"timeout_ms,omitempty"`
	MaxRetries    int   `json:"max_retries,omitempty"`
	BackoffBaseMs int   `json:"backoff_base_ms,omitempty"`
	BackoffMaxMs  int   `json:"backoff_max_ms,omitempty"`
	RetryOn429    *bool `json:"retry_on_429,omitempty"`

	SecretMasked string    `json:"secret_masked"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type targetListResponse struct {
	Items []targetDTO `json:"items"`
}

type triggerEventResponse struct {
	Enqueued int    `json:"enqueued"`
	Event    string `json:"event"`
}

type deliveryListItemDTO struct {
	ID           string         `json:"id"`
	TargetID     string         `json:"target_id"`
	EventID      string         `json:"event_id"`
	EventType    string         `json:"event_type"`
	Status       DeliveryStatus `json:"status"`
	Attempt      int            `json:"attempt"`
	NextAt       *time.Time     `json:"next_at,omitempty"`
	LastHTTP     int            `json:"last_http_status,omitempty"`
	LastError    string         `json:"last_error,omitempty"`
	LastDuration int            `json:"last_duration_ms,omitempty"`
	LastResp     string         `json:"last_resp_snippet,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type deliveryListResponse struct {
	Items []deliveryListItemDTO `json:"items"`
}

type deliveryDetailResponse struct {
	ID           string          `json:"id"`
	TargetID     string          `json:"target_id"`
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	Status       DeliveryStatus  `json:"status"`
	Attempt      int             `json:"attempt"`
	NextAt       *time.Time      `json:"next_at,omitempty"`
	LastHTTP     int             `json:"last_http_status,omitempty"`
	LastError    string          `json:"last_error,omitempty"`
	LastDuration int             `json:"last_duration_ms,omitempty"`
	LastResp     string          `json:"last_resp_snippet,omitempty"`
	PayloadJSON  json.RawMessage `json:"payload_json"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type replayDeliveryResponse struct {
	OK         bool   `json:"ok"`
	DeliveryID string `json:"delivery_id"`
}

func webhookCreateTarget(w http.ResponseWriter, r *http.Request, svc *Service) {
	var req Target
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWebhookInvalidJSONError(w, r)
		return
	}

	t, err := svc.CreateTarget(r.Context(), req)
	if err != nil {
		writeWebhookValidationError(w, r, "invalid webhook target")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusCreated, targetToDTO(t), nil)
}

func webhookListTargets(w http.ResponseWriter, r *http.Request, svc *Service) {
	q := r.URL.Query()

	enabled, ok := webhookOptionalBoolQuery(w, r, "enabled")
	if !ok {
		return
	}
	event := strings.TrimSpace(q.Get("event"))

	items, err := svc.ListTargets(r.Context(), TargetFilter{
		Enabled: enabled,
		Event:   event,
	})
	if err != nil {
		writeWebhookInternalError(w, r, "webhook targets unavailable")
		return
	}

	out := make([]targetDTO, 0, len(items))
	for _, t := range items {
		out = append(out, targetToDTO(t))
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, targetListResponse{Items: out}, nil)
}

func webhookGetTarget(w http.ResponseWriter, r *http.Request, svc *Service) {
	id, ok := webhookRequiredRouteParam(w, r, "id")
	if !ok {
		return
	}

	t, ok := svc.GetTarget(r.Context(), id)
	if !ok {
		writeWebhookNotFoundError(w, r, "target not found")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, targetToDTO(t), nil)
}

func webhookPatchTarget(w http.ResponseWriter, r *http.Request, svc *Service) {
	id, ok := webhookRequiredRouteParam(w, r, "id")
	if !ok {
		return
	}

	var req TargetPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWebhookInvalidJSONError(w, r)
		return
	}

	t, err := svc.UpdateTarget(r.Context(), id, req)
	if err != nil {
		writeWebhookValidationError(w, r, "invalid webhook target update")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, targetToDTO(t), nil)
}

func webhookSetTargetEnabled(w http.ResponseWriter, r *http.Request, svc *Service, enable bool) {
	id, ok := webhookRequiredRouteParam(w, r, "id")
	if !ok {
		return
	}

	t, err := svc.UpdateTarget(r.Context(), id, TargetPatch{Enabled: &enable})
	if err != nil {
		if errors.Is(err, ErrTargetNotFound) {
			writeWebhookNotFoundError(w, r, "target not found")
			return
		}
		writeWebhookValidationError(w, r, "invalid webhook target update")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, targetToDTO(t), nil)
}

func webhookTriggerEvent(w http.ResponseWriter, r *http.Request, svc *Service, token string, allowEmpty bool) {
	event, ok := webhookRequiredRouteParam(w, r, "event")
	if !ok {
		return
	}

	provided := strings.TrimSpace(r.URL.Query().Get("token"))
	if provided == "" {
		provided = strings.TrimSpace(r.Header.Get("X-Trigger-Token"))
	}

	if token == "" && !allowEmpty {
		writeWebhookForbiddenError(w, r, "triggering is disabled")
		return
	}
	if token != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		writeWebhookUnauthorizedError(w, r, "invalid trigger token")
		return
	}

	var payload struct {
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeWebhookInvalidJSONError(w, r)
		return
	}

	enqueued, err := svc.TriggerEvent(r.Context(), Event{Type: event, Data: payload.Data, Meta: payload.Meta})
	if err != nil {
		writeWebhookValidationError(w, r, "invalid webhook event")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusAccepted, triggerEventResponse{
		Enqueued: enqueued,
		Event:    event,
	}, nil)
}

func webhookListDeliveries(w http.ResponseWriter, r *http.Request, svc *Service, defaultLimit int) {
	q := r.URL.Query()

	limit := defaultLimit
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	filter := DeliveryFilter{Limit: limit, Cursor: strings.TrimSpace(q.Get("cursor"))}

	if v := strings.TrimSpace(q.Get("target_id")); v != "" {
		filter.TargetID = &v
	}
	if v := strings.TrimSpace(q.Get("event")); v != "" {
		filter.Event = &v
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		status := DeliveryStatus(v)
		filter.Status = &status
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			filter.From = &ts
		}
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		if ts, err := time.Parse(time.RFC3339, v); err == nil {
			filter.To = &ts
		}
	}

	deliveries, err := svc.ListDeliveries(r.Context(), filter)
	if err != nil {
		writeWebhookInternalError(w, r, "webhook deliveries unavailable")
		return
	}

	out := make([]deliveryListItemDTO, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, deliveryListItemDTO{
			ID:           d.ID,
			TargetID:     d.TargetID,
			EventID:      d.EventID,
			EventType:    d.EventType,
			Status:       d.Status,
			Attempt:      d.Attempt,
			NextAt:       d.NextAt,
			LastHTTP:     d.LastHTTPStatus,
			LastError:    d.LastError,
			LastDuration: d.LastDurationMs,
			LastResp:     d.LastRespSnippet,
			CreatedAt:    d.CreatedAt,
			UpdatedAt:    d.UpdatedAt,
		})
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, deliveryListResponse{Items: out}, nil)
}

func webhookGetDelivery(w http.ResponseWriter, r *http.Request, svc *Service) {
	id, ok := webhookRequiredRouteParam(w, r, "id")
	if !ok {
		return
	}

	d, ok := svc.GetDelivery(r.Context(), id)
	if !ok {
		writeWebhookNotFoundError(w, r, "delivery not found")
		return
	}

	payload := json.RawMessage(d.PayloadJSON)

	_ = contract.WriteResponse(w, r, http.StatusOK, deliveryDetailResponse{
		ID:           d.ID,
		TargetID:     d.TargetID,
		EventID:      d.EventID,
		EventType:    d.EventType,
		Status:       d.Status,
		Attempt:      d.Attempt,
		NextAt:       d.NextAt,
		LastHTTP:     d.LastHTTPStatus,
		LastError:    d.LastError,
		LastDuration: d.LastDurationMs,
		LastResp:     d.LastRespSnippet,
		PayloadJSON:  payload,
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
	}, nil)
}

func webhookReplayDelivery(w http.ResponseWriter, r *http.Request, svc *Service) {
	id, ok := webhookRequiredRouteParam(w, r, "id")
	if !ok {
		return
	}

	d, err := svc.ReplayDelivery(r.Context(), id)
	if errors.Is(err, ErrTargetNotFound) {
		writeWebhookNotFoundError(w, r, "delivery not found")
		return
	}
	if err != nil {
		writeWebhookValidationError(w, r, "invalid webhook delivery replay")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, replayDeliveryResponse{OK: true, DeliveryID: d.ID}, nil)
}

func webhookRequiredRouteParam(w http.ResponseWriter, r *http.Request, field string) (string, bool) {
	value := strings.TrimSpace(contract.RequestContextFromContext(r.Context()).Params[field])
	if value == "" {
		writeWebhookRequiredError(w, r, field)
		return "", false
	}
	return value, true
}

func webhookOptionalBoolQuery(w http.ResponseWriter, r *http.Request, field string) (*bool, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(field))
	if value == "" {
		return nil, true
	}

	var parsed bool
	switch strings.ToLower(value) {
	case "1", "true":
		parsed = true
	case "0", "false":
		parsed = false
	default:
		writeWebhookValidationError(w, r, "invalid "+field+" query")
		return nil, false
	}
	return &parsed, true
}

func targetToDTO(t Target) targetDTO {
	dto := targetDTO{
		ID:            t.ID,
		Name:          t.Name,
		URL:           t.URL,
		Events:        t.Events,
		Enabled:       t.Enabled,
		Headers:       t.Headers,
		SecretMasked:  stringsx.MaskSecret(t.Secret),
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		TimeoutMs:     t.TimeoutMs,
		MaxRetries:    t.MaxRetries,
		BackoffBaseMs: t.BackoffBaseMs,
		BackoffMaxMs:  t.BackoffMaxMs,
	}

	if t.RetryOn429 != nil {
		dto.RetryOn429 = t.RetryOn429
	}

	return dto
}

func writeWebhookRequiredError(w http.ResponseWriter, r *http.Request, field string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeRequired).
		Message(field+" is required").
		Detail("field", field).
		Build())
}

func writeWebhookNotFoundError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message(message).
		Build())
}

func writeWebhookForbiddenError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeForbidden).
		Message(message).
		Build())
}

func writeWebhookUnauthorizedError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeUnauthorized).
		Message(message).
		Build())
}

func writeWebhookInvalidJSONError(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeInvalidJSON).
		Message("invalid JSON payload").
		Build())
}

func writeWebhookValidationError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeBadRequest).
		Message(message).
		Build())
}

func writeWebhookInternalError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Code(contract.CodeInternalError).
		Message(message).
		Build())
}
