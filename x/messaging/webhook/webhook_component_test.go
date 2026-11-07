package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/router"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

func TestWebhookBridge(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	// Test with nil PubSub
	bridgeBad := &WebhookBridge{
		Out:   nil,
		Rules: []Rule{{InTopic: "test", OutEventType: "test"}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err := bridgeBad.Start(ctx)
	if err == nil {
		t.Error("expected error for nil PubSub")
	}

	// Test with nil Service
	bridgeBad2 := &WebhookBridge{
		Pub:   ps,
		Rules: []Rule{{InTopic: "test", OutEventType: "test"}},
	}
	_, err = bridgeBad2.Start(ctx)
	if err == nil {
		t.Error("expected error for nil Service")
	}

	// Test with no rules
	bridgeBad3 := &WebhookBridge{
		Pub: ps,
		Out: nil,
	}
	_, err = bridgeBad3.Start(ctx)
	if err == nil {
		t.Error("expected error for no rules")
	}
}

func TestWebhookBridgeStopAll(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	bridge := &WebhookBridge{
		Pub:   ps,
		Out:   nil,
		Rules: []Rule{},
	}

	// Test stopAll when not started
	bridge.stopAll()

	// Test stopAll when subs is nil
	bridge.subs = nil
	bridge.stopAll()

	// Test stopAll with empty subs
	bridge.subs = []pubsub.Subscription{}
	bridge.stopAll()
}

func TestWebhookBridgeFirstNonEmpty(t *testing.T) {
	// Test the firstNonEmpty helper function
	tests := []struct {
		a, b     string
		expected string
	}{
		{"", "", ""},
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a"},
	}

	for _, tt := range tests {
		result := firstNonEmpty(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("firstNonEmpty(%q, %q) = %q, want %q", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestWebhookInboundBasic(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	cfg := WebhookInConfig{
		Enabled:      true,
		Pub:          ps,
		GitHubSecret: "secret123",
		StripeSecret: "secret456",
		GitHubPath:   "/webhooks/github",
		StripePath:   "/webhooks/stripe",
	}

	handler := NewInbound(cfg, ps, nil)

	// Test Health
	name, health := handler.Health()
	if name != "webhook_in" {
		t.Errorf("expected name 'webhook_in', got %s", name)
	}
	if health.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", health.Status)
	}

	// Test disabled component
	disabledCfg := WebhookInConfig{Enabled: false}
	disabledHandler := NewInbound(disabledCfg, ps, nil)
	_, disabledHealth := disabledHandler.Health()
	if disabledHealth.Status != "degraded" {
		t.Errorf("expected degraded status for disabled component, got %s", disabledHealth.Status)
	}
}

func TestWebhookOutboundBasic(t *testing.T) {
	cfg := WebhookOutConfig{
		Enabled:  true,
		BasePath: "/webhooks",
	}

	handler := NewOutbound(cfg)

	// Test Health
	name, health := handler.Health()
	if name != "webhook_out" {
		t.Errorf("expected name 'webhook_out', got %s", name)
	}
	if health.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", health.Status)
	}

	// Test disabled component
	disabledCfg := WebhookOutConfig{Enabled: false}
	disabledHandler := NewOutbound(disabledCfg)
	_, disabledHealth := disabledHandler.Health()
	if disabledHealth.Status != "degraded" {
		t.Errorf("expected degraded status for disabled component, got %s", disabledHealth.Status)
	}
}

func TestInboundRegisterRoutesDuplicateReturnsError(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	handler := NewInbound(WebhookInConfig{
		Enabled:      true,
		Pub:          ps,
		GitHubSecret: "secret123",
		StripeSecret: "secret456",
	}, ps, nil)

	r := router.NewRouter()
	if err := handler.RegisterRoutes(r); err != nil {
		t.Fatalf("first RegisterRoutes failed: %v", err)
	}
	if err := handler.RegisterRoutes(r); err == nil {
		t.Fatal("expected duplicate RegisterRoutes to return an error")
	}
}

func TestOutboundRegisterRoutesDuplicateReturnsError(t *testing.T) {
	handler := NewOutbound(WebhookOutConfig{
		Enabled: true,
		Service: &Service{},
	})

	r := router.NewRouter()
	if err := handler.RegisterRoutes(r); err != nil {
		t.Fatalf("first RegisterRoutes failed: %v", err)
	}
	if err := handler.RegisterRoutes(r); err == nil {
		t.Fatal("expected duplicate RegisterRoutes to return an error")
	}
}

func TestInboundMissingSecretWritesCanonicalError(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	handler := NewInbound(WebhookInConfig{
		Enabled:    true,
		Pub:        ps,
		GitHubPath: "/webhooks/github",
	}, ps, nil)

	r := router.NewRouter()
	if err := handler.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "missing_secret" {
		t.Fatalf("error code = %q, want missing_secret", body.Error.Code)
	}
}

func TestInboundGitHubSuccessResponseShapeAndDedup(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	handler := NewInbound(WebhookInConfig{
		Enabled:           true,
		Pub:               ps,
		GitHubSecret:      "secret123",
		TopicPrefixGitHub: "in.github.",
	}, ps, nil)

	body := []byte(`{"ok":true}`)
	req := signedGitHubRequest(body, "secret123", "push", "delivery-1")
	rec := httptest.NewRecorder()
	handler.webhookInGitHub(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("github status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp := decodeWebhookComponentData[inboundWebhookResponse](t, rec)
	if !resp.OK || resp.Provider != "github" || resp.Topic != "in.github.push" || resp.EventType != "push" || resp.DeliveryID != "delivery-1" || resp.Deduped || resp.BodyBytes != len(body) {
		t.Fatalf("unexpected github response: %+v", resp)
	}

	req = signedGitHubRequest(body, "secret123", "push", "delivery-1")
	rec = httptest.NewRecorder()
	handler.webhookInGitHub(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("github dedupe status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp = decodeWebhookComponentData[inboundWebhookResponse](t, rec)
	if !resp.OK || resp.Provider != "github" || resp.Topic != "" || !resp.Deduped || resp.BodyBytes != 0 {
		t.Fatalf("unexpected github dedupe response: %+v", resp)
	}
}

func TestInboundStripeSuccessResponseShapeAndDedup(t *testing.T) {
	ps := pubsub.New()
	defer func() { _ = ps.Close() }()

	now := time.Now().UTC()
	handler := NewInbound(WebhookInConfig{
		Enabled:           true,
		Pub:               ps,
		StripeSecret:      "whsec_secret",
		StripeTolerance:   time.Minute,
		TopicPrefixStripe: "in.stripe.",
	}, ps, nil)

	body := []byte(`{"id":"evt_1","type":"payment_intent.succeeded"}`)
	req := signedStripeRequest(body, "whsec_secret", now)
	rec := httptest.NewRecorder()
	handler.webhookInStripe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stripe status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp := decodeWebhookComponentData[inboundWebhookResponse](t, rec)
	if !resp.OK || resp.Provider != "stripe" || resp.Topic != "in.stripe.payment_intent.succeeded" || resp.EventType != "payment_intent.succeeded" || resp.EventID != "evt_1" || resp.Deduped || resp.BodyBytes != len(body) {
		t.Fatalf("unexpected stripe response: %+v", resp)
	}

	req = signedStripeRequest(body, "whsec_secret", now)
	rec = httptest.NewRecorder()
	handler.webhookInStripe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stripe dedupe status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	resp = decodeWebhookComponentData[inboundWebhookResponse](t, rec)
	if !resp.OK || resp.Provider != "stripe" || resp.Topic != "" || !resp.Deduped || resp.BodyBytes != 0 {
		t.Fatalf("unexpected stripe dedupe response: %+v", resp)
	}
}

func TestOutboundTriggerDisabledWritesCanonicalError(t *testing.T) {
	handler := NewOutbound(WebhookOutConfig{
		Enabled:         true,
		Service:         &Service{},
		AllowEmptyToken: false,
	})

	r := router.NewRouter()
	if err := handler.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/events/demo", httptest.NewRequest(http.MethodPost, "/", nil).Body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "FORBIDDEN" {
		t.Fatalf("error code = %q, want FORBIDDEN", body.Error.Code)
	}
}

func TestOutboundHandlersUseCanonicalErrorCodes(t *testing.T) {
	svc := NewService(NewMemStore(), Config{})

	t.Run("missing target id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhooks/targets/", nil)
		rec := httptest.NewRecorder()

		webhookGetTarget(rec, req, svc)

		assertWebhookErrorCode(t, rec, http.StatusBadRequest, contract.CodeRequired, "id is required")
	})

	t.Run("target not found", func(t *testing.T) {
		req := requestWithWebhookParam(http.MethodGet, "/webhooks/targets/missing", strings.NewReader(""), "id", "missing")
		rec := httptest.NewRecorder()

		webhookGetTarget(rec, req, svc)

		assertWebhookErrorCode(t, rec, http.StatusNotFound, contract.CodeResourceNotFound, "target not found")
	})

	t.Run("unauthorized trigger token", func(t *testing.T) {
		req := requestWithWebhookParam(http.MethodPost, "/webhooks/events/demo", strings.NewReader(`{"data":{}}`), "event", "demo")
		rec := httptest.NewRecorder()

		webhookTriggerEvent(rec, req, svc, "expected-token", false)

		assertWebhookErrorCode(t, rec, http.StatusUnauthorized, contract.CodeUnauthorized, "invalid trigger token")
	})

	t.Run("invalid target JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/targets", strings.NewReader("{"))
		rec := httptest.NewRecorder()

		webhookCreateTarget(rec, req, svc)

		assertWebhookErrorCode(t, rec, http.StatusBadRequest, contract.CodeInvalidJSON, "invalid JSON payload")
	})

	t.Run("target validation failure", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/targets", strings.NewReader(`{"name":"","url":"https://example.com","secret":"abcdefgh","events":["demo"]}`))
		rec := httptest.NewRecorder()

		webhookCreateTarget(rec, req, svc)

		assertWebhookErrorCode(t, rec, http.StatusBadRequest, contract.CodeBadRequest, "invalid webhook target")
	})
}

func TestOutboundListHandlersSanitizeStoreErrors(t *testing.T) {
	svc := NewService(failingListStore{Store: NewMemStore()}, Config{})

	t.Run("targets", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhooks/targets", nil)
		rec := httptest.NewRecorder()
		webhookListTargets(rec, req, svc)

		assertWebhookStableInternalError(t, rec, "webhook targets unavailable")
	})

	t.Run("deliveries", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/webhooks/deliveries", nil)
		rec := httptest.NewRecorder()
		webhookListDeliveries(rec, req, svc, 20)

		assertWebhookStableInternalError(t, rec, "webhook deliveries unavailable")
	})
}

func requestWithWebhookParam(method, target string, body *strings.Reader, key, value string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	ctx := contract.WithRequestContext(req.Context(), contract.RequestContext{
		Params: map[string]string{key: value},
	})
	return req.WithContext(ctx)
}

func signedGitHubRequest(body []byte, secret, event, delivery string) *http.Request {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	return req
}

func signedStripeRequest(body []byte, secret string, now time.Time) *http.Request {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", "t="+ts+",v1="+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func decodeWebhookComponentData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}

func assertWebhookErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != code {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, code)
	}
	if resp.Error.Message != message {
		t.Fatalf("error message = %q, want %q", resp.Error.Message, message)
	}
}

type failingListStore struct {
	Store
}

func (s failingListStore) ListTargets(ctx context.Context, filter TargetFilter) ([]Target, error) {
	return nil, errors.New("backend dsn secret leaked")
}

func (s failingListStore) ListDeliveries(ctx context.Context, filter DeliveryFilter) ([]Delivery, error) {
	return nil, errors.New("backend dsn secret leaked")
}

func assertWebhookStableInternalError(t *testing.T, rec *httptest.ResponseRecorder, wantMessage string) {
	t.Helper()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != contract.CodeInternalError {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, contract.CodeInternalError)
	}
	if resp.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", resp.Error.Message, wantMessage)
	}
	if strings.Contains(resp.Error.Message, "secret") {
		t.Fatalf("error message leaked backend detail: %q", resp.Error.Message)
	}
}
