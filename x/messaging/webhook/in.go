package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
	"github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type routeRegistrar interface {
	AddRoute(method, path string, handler http.Handler, opts ...router.RouteOption) error
}

// HTTP-layer error codes used in inbound webhook handler responses.
const (
	httpCodeMissingSecret    = "missing_secret"
	httpCodeInvalidSignature = "invalid_signature"
	httpCodePublishFailed    = "publish_failed"
)

type Inbound struct {
	cfg            WebhookInConfig
	pub            pubsub.Broker
	logger         log.StructuredLogger
	deduper        *Deduper
	replayWarnOnce sync.Once
}

type inboundWebhookResponse struct {
	OK         bool   `json:"ok"`
	Provider   string `json:"provider"`
	Topic      string `json:"topic,omitempty"`
	EventType  string `json:"event_type"`
	DeliveryID string `json:"delivery_id,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	Deduped    bool   `json:"deduped"`
	BodyBytes  int    `json:"body_bytes,omitempty"`
}

func extractStripeEventIdentity(raw []byte) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	eventID, _ := payload["id"].(string)
	eventType, _ := payload["type"].(string)
	return strings.TrimSpace(eventID), strings.TrimSpace(eventType)
}

func NewInbound(cfg WebhookInConfig, fallbackPub pubsub.Broker, logger log.StructuredLogger) *Inbound {
	if logger == nil {
		logger = log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	}
	pub := cfg.Pub
	if pub == nil {
		pub = fallbackPub
	}

	return &Inbound{cfg: cfg, pub: pub, logger: logger}
}

func (c *Inbound) RegisterRoutes(r routeRegistrar) error {
	if !c.cfg.Enabled || c.pub == nil {
		return nil
	}

	gitHubPath := strings.TrimSpace(c.cfg.GitHubPath)
	if gitHubPath == "" {
		gitHubPath = "/webhooks/github"
	}
	if err := r.AddRoute(http.MethodPost, gitHubPath, http.HandlerFunc(c.webhookInGitHub)); err != nil {
		return err
	}

	stripePath := strings.TrimSpace(c.cfg.StripePath)
	if stripePath == "" {
		stripePath = "/webhooks/stripe"
	}
	return r.AddRoute(http.MethodPost, stripePath, http.HandlerFunc(c.webhookInStripe))
}

func (c *Inbound) Health() (string, health.HealthStatus) {
	status := health.HealthStatus{Status: health.StatusHealthy, Details: map[string]any{"enabled": c.cfg.Enabled}}

	if !c.cfg.Enabled {
		status.Status = health.StatusDegraded
		status.Message = "component disabled"
	}

	return "webhook_in", status
}

func (c *Inbound) webhookInGitHub(w http.ResponseWriter, r *http.Request) {
	c.replayWarnOnce.Do(func() {
		c.logger.Warn("NonceStore not configured: replay attacks are possible for verified webhooks", log.Fields{"provider": "github"})
	})
	secret := strings.TrimSpace(c.cfg.GitHubSecret)
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	}
	if secret == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(httpCodeMissingSecret).
			Message("GITHUB_WEBHOOK_SECRET is not configured").
			Build())
		return
	}

	maxBody := c.cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	raw, err := VerifyGitHub(r, secret, maxBody)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnauthorized).
			Code(httpCodeInvalidSignature).
			Message("invalid GitHub signature").
			Build())
		return
	}

	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if event == "" {
		event = "unknown"
	}
	if delivery == "" {
		delivery = "unknown"
	}

	d := c.ensureWebhookInDeduper()
	if delivery != "unknown" && d.SeenBefore("github:"+delivery) {
		_ = contract.WriteResponse(w, r, http.StatusOK, inboundWebhookResponse{
			OK:         true,
			Provider:   "github",
			EventType:  event,
			DeliveryID: delivery,
			Deduped:    true,
		}, nil)
		return
	}

	topicPrefix := strings.TrimSpace(c.cfg.TopicPrefixGitHub)
	if topicPrefix == "" {
		topicPrefix = "in.github."
	}
	topic := topicPrefix + sanitizeTopicSuffix(event)

	msg := pubsub.Message{
		ID:    delivery,
		Topic: topic,
		Type:  event,
		Time:  time.Now(),
		Data:  json.RawMessage(raw),
		Meta: map[string]string{
			"source":      "github",
			"request_id":  contract.RequestIDFromContext(r.Context()),
			"delivery_id": delivery,
			"event_type":  event,
			"client_ip":   httputil.ClientIP(r),
		},
	}

	if err = c.pub.Publish(topic, msg); err != nil {
		c.logger.Error("Failed to publish GitHub event", log.Fields{"error_type": fmt.Sprintf("%T", err), "topic": topic, "event_id": delivery})
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(httpCodePublishFailed).
			Message("failed to forward event to internal bus").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, inboundWebhookResponse{
		OK:         true,
		Provider:   "github",
		Topic:      topic,
		EventType:  event,
		DeliveryID: delivery,
		Deduped:    false,
		BodyBytes:  len(raw),
	}, nil)
}

func (c *Inbound) webhookInStripe(w http.ResponseWriter, r *http.Request) {
	c.replayWarnOnce.Do(func() {
		c.logger.Warn("NonceStore not configured: replay attacks are possible for verified webhooks", log.Fields{"provider": "stripe"})
	})
	secret := strings.TrimSpace(c.cfg.StripeSecret)
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET"))
	}
	if secret == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(httpCodeMissingSecret).
			Message("STRIPE_WEBHOOK_SECRET is not configured").
			Build())
		return
	}

	maxBody := c.cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	tol := c.cfg.StripeTolerance
	if tol <= 0 {
		tol = 5 * time.Minute
	}

	raw, err := VerifyStripe(r, secret, StripeVerifyOptions{MaxBody: maxBody, Tolerance: tol})
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnauthorized).
			Code(httpCodeInvalidSignature).
			Message("invalid Stripe signature").
			Build())
		return
	}

	evtID, evtType := extractStripeEventIdentity(raw)
	if evtType == "" {
		evtType = "unknown"
	}
	if evtID == "" {
		evtID = "unknown"
	}

	d := c.ensureWebhookInDeduper()
	if evtID != "unknown" && d.SeenBefore("stripe:"+evtID) {
		_ = contract.WriteResponse(w, r, http.StatusOK, inboundWebhookResponse{
			OK:        true,
			Provider:  "stripe",
			EventType: evtType,
			EventID:   evtID,
			Deduped:   true,
		}, nil)
		return
	}

	topicPrefix := strings.TrimSpace(c.cfg.TopicPrefixStripe)
	if topicPrefix == "" {
		topicPrefix = "in.stripe."
	}
	topic := topicPrefix + sanitizeTopicSuffix(evtType)

	msg := pubsub.Message{
		ID:    evtID,
		Topic: topic,
		Type:  evtType,
		Time:  time.Now(),
		Data:  json.RawMessage(raw),
		Meta: map[string]string{
			"source":      "stripe",
			"request_id":  contract.RequestIDFromContext(r.Context()),
			"delivery_id": evtID,
			"event_type":  evtType,
			"client_ip":   httputil.ClientIP(r),
		},
	}

	if err = c.pub.Publish(topic, msg); err != nil {
		c.logger.Error("Failed to publish Stripe event", log.Fields{"error_type": fmt.Sprintf("%T", err), "topic": topic, "event_id": evtID})
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(httpCodePublishFailed).
			Message("failed to forward event to internal bus").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, inboundWebhookResponse{
		OK:        true,
		Provider:  "stripe",
		Topic:     topic,
		EventType: evtType,
		EventID:   evtID,
		Deduped:   false,
		BodyBytes: len(raw),
	}, nil)
}

func (c *Inbound) ensureWebhookInDeduper() *Deduper {
	if c.cfg.Deduper != nil {
		return c.cfg.Deduper
	}
	if c.deduper != nil {
		return c.deduper
	}
	ttl := c.cfg.DedupTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	c.deduper = NewDeduper(ttl)
	return c.deduper
}

// sanitizeTopicSuffix keeps topic safe and consistent.
// Allow [a-zA-Z0-9._-], replace others with '_'.
func sanitizeTopicSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-'
		if ok {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.ToLower(b.String())
}
