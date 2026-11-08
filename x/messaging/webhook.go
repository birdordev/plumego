package messaging

import (
	"context"
	"encoding/json"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/messaging/webhook"
)

// WebhookNotifier subscribes to "messaging.result" events on the pubsub bus
// and triggers outbound webhooks via webhook.Service.
type WebhookNotifier struct {
	webhook *webhook.Service
	bus     *pubsub.InProcBroker
	logger  log.StructuredLogger
	sub     pubsub.Subscription
}

// NewWebhookNotifier creates a notifier wired to the given services.
func NewWebhookNotifier(bus *pubsub.InProcBroker, webhook *webhook.Service, logger log.StructuredLogger) *WebhookNotifier {
	return &WebhookNotifier{
		webhook: webhook,
		bus:     bus,
		logger:  logger,
	}
}

// Start subscribes to messaging result events and forwards them.
func (n *WebhookNotifier) Start(ctx context.Context) error {
	if n.bus == nil || n.webhook == nil {
		return nil
	}
	sub, err := n.bus.Subscribe(ctx, "messaging.result", pubsub.SubOptions{BufferSize: 64})
	if err != nil {
		return err
	}
	n.sub = sub
	go n.loop(ctx)
	return nil
}

// Stop cancels the subscription.
func (n *WebhookNotifier) Stop() {
	if n.sub != nil {
		n.sub.Cancel()
	}
}

func (n *WebhookNotifier) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.sub.Done():
			return
		case msg, ok := <-n.sub.C():
			if !ok {
				return
			}
			n.forward(ctx, msg)
		}
	}
}

func (n *WebhookNotifier) forward(ctx context.Context, msg pubsub.Message) {
	var result SendResult
	data, _ := msg.Data.(string)
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		if n.logger != nil {
			n.logger.Warn("webhook notifier: unmarshal result", log.Fields{"error": err.Error()})
		}
		return
	}

	eventType := "messaging." + string(result.Channel) + "." + result.Status

	eventData := map[string]any{
		"request_id":  result.RequestID,
		"channel":     result.Channel,
		"status":      result.Status,
		"provider_id": result.ProviderID,
		"attempts":    result.Attempts,
	}
	if !result.SentAt.IsZero() {
		eventData["sent_at"] = result.SentAt
	}
	if result.Error != "" {
		eventData["error"] = result.Error
	}

	_, err := n.webhook.TriggerEvent(ctx, webhook.Event{
		Type:       eventType,
		OccurredAt: time.Now(),
		Data:       eventData,
	})
	if err != nil && n.logger != nil {
		n.logger.Warn("webhook notifier: trigger failed", log.Fields{
			"event": eventType,
			"error": err.Error(),
		})
	}
}
