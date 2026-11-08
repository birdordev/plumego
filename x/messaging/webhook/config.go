package webhook

import (
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// WebhookOutConfig configures the outbound webhook management endpoints.
type WebhookOutConfig struct {
	Enabled bool     // Whether to enable the outbound webhook endpoints
	Service *Service // Webhook service instance

	// TriggerToken protects event triggering. When empty and AllowEmptyToken is false, triggers are forbidden.
	TriggerToken     string // Token for event triggering
	AllowEmptyToken  bool   // Whether to allow empty trigger token
	BasePath         string // Base path for webhook endpoints
	IncludeStats     bool   // Whether to include stats in responses
	DefaultPageLimit int    // Default page limit for paginated responses
}

// WebhookInConfig configures inbound webhook receivers.
type WebhookInConfig struct {
	Enabled bool          // Whether to enable the inbound webhook endpoints
	Pub     pubsub.Broker // PubSub instance for event publishing

	GitHubSecret      string        // Secret for GitHub webhook events
	StripeSecret      string        // Secret for Stripe webhook events
	MaxBodyBytes      int64         // Maximum allowed request body size
	StripeTolerance   time.Duration // Tolerance for Stripe webhook event timestamps
	DedupTTL          time.Duration // Time-to-live for deduplication cache
	Deduper           *Deduper      // Deduper instance for event deduplication
	GitHubPath        string        // Path for GitHub webhook events
	StripePath        string        // Path for Stripe webhook events
	TopicPrefixGitHub string        // Prefix for GitHub event topics
	TopicPrefixStripe string        // Prefix for Stripe event topics
}
