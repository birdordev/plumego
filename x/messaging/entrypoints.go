package messaging

import (
	"net/http"

	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/messaging/webhook"
)

// Broker is the canonical in-process messaging broker type for application code.
type Broker = pubsub.InProcBroker

// BrokerOption configures an in-process messaging broker.
type BrokerOption = pubsub.Option

// BrokerConfig describes broker tuning.
type BrokerConfig = pubsub.Config

// BrokerMessage is a published in-process messaging event.
type BrokerMessage = pubsub.Message

// BrokerSubscription is an active broker subscription.
type BrokerSubscription = pubsub.Subscription

// BrokerSubOptions configures a broker subscription.
type BrokerSubOptions = pubsub.SubOptions

// BrokerBackpressurePolicy controls broker overflow handling.
type BrokerBackpressurePolicy = pubsub.BackpressurePolicy

// NewInProcBroker creates the canonical in-process broker for messaging-family work.
func NewInProcBroker(opts ...BrokerOption) *Broker {
	return pubsub.New(opts...)
}

// WebhookService is the outbound webhook service used by the messaging family.
type WebhookService = webhook.Service

// WebhookStore persists outbound webhook targets and deliveries.
type WebhookStore = webhook.Store

// WebhookConfig configures the outbound webhook service.
type WebhookConfig = webhook.Config

// WebhookEvent is an outbound webhook event payload.
type WebhookEvent = webhook.Event

// WebhookDelivery captures an outbound delivery attempt.
type WebhookDelivery = webhook.Delivery

// WebhookMemStore is the in-memory outbound webhook store.
type WebhookMemStore = webhook.MemStore

// WebhookHMACConfig configures generic inbound HMAC verification.
type WebhookHMACConfig = webhook.HMACConfig

// WebhookVerifyResult reports the result of inbound HMAC verification.
type WebhookVerifyResult = webhook.VerifyResult

// GitHubVerifyOptions configures GitHub webhook verification.
type GitHubVerifyOptions = webhook.GitHubVerifyOptions

// StripeVerifyOptions configures Stripe webhook verification.
type StripeVerifyOptions = webhook.StripeVerifyOptions

// NewWebhookService creates the canonical outbound webhook service for messaging-family work.
func NewWebhookService(store WebhookStore, cfg WebhookConfig) *WebhookService {
	return webhook.NewService(store, cfg)
}

// NewWebhookMemStore creates an in-memory outbound webhook store.
func NewWebhookMemStore() *WebhookMemStore {
	return webhook.NewMemStore()
}

// VerifyWebhookHMAC verifies a generic HMAC-signed inbound webhook request.
func VerifyWebhookHMAC(r *http.Request, cfg WebhookHMACConfig) (WebhookVerifyResult, error) {
	return webhook.VerifyHMAC(r, cfg)
}

// VerifyGitHubWebhook verifies a GitHub webhook request.
func VerifyGitHubWebhook(r *http.Request, secret string, maxBody int64) ([]byte, error) {
	return webhook.VerifyGitHub(r, secret, maxBody)
}

// VerifyStripeWebhook verifies a Stripe webhook request.
func VerifyStripeWebhook(r *http.Request, endpointSecret string, opt StripeVerifyOptions) ([]byte, error) {
	return webhook.VerifyStripe(r, endpointSecret, opt)
}
