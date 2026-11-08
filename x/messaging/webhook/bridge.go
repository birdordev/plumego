package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type Rule struct {
	// Inbound PubSub topic, e.g. "in.github.push"
	InTopic string

	// Outbound event type for webhook.TriggerEvent, e.g. "github.push"
	OutEventType string

	// Optional: override Meta["source"] if you want.
	Source string
}

type WebhookBridge struct {
	Pub pubsub.Broker
	Out *Service

	// Rules define what inbound topics are bridged to which outbound event types.
	Rules []Rule

	// Subscriber options for bridge consumers
	SubOptions pubsub.SubOptions

	// Internal
	mu      sync.Mutex
	subs    []pubsub.Subscription
	started bool
}

func (b *WebhookBridge) Start(ctx context.Context) (stop func() error, err error) {
	if b.Pub == nil {
		return nil, errors.New("bridge: PubSub is nil")
	}
	if b.Out == nil {
		return nil, errors.New("bridge: webhook.Service is nil")
	}
	if len(b.Rules) == 0 {
		return nil, errors.New("bridge: no rules configured")
	}

	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return nil, errors.New("bridge: already started")
	}
	b.started = true
	b.mu.Unlock()

	opts := b.SubOptions
	if opts.BufferSize <= 0 {
		opts.BufferSize = 64
	}
	// Safe default for bridge workers
	if opts.Policy == 0 {
		opts.Policy = pubsub.DropOldest
	}

	// Start subscriptions
	type worker struct {
		sub  pubsub.Subscription
		rule Rule
	}
	workers := make([]worker, 0, len(b.Rules))

	for _, r := range b.Rules {
		in := strings.TrimSpace(r.InTopic)
		out := strings.TrimSpace(r.OutEventType)
		if in == "" || out == "" {
			continue
		}
		sub, e := b.Pub.Subscribe(context.Background(), in, opts)
		if e != nil {
			// best effort: stop already started subs
			b.stopAll()
			return nil, e
		}
		workers = append(workers, worker{sub: sub, rule: r})

		b.mu.Lock()
		b.subs = append(b.subs, sub)
		b.mu.Unlock()
	}

	// Run workers
	var wg sync.WaitGroup
	wg.Add(len(workers))

	for _, w := range workers {
		go func(w worker) {
			defer wg.Done()
			b.runWorker(ctx, w.sub, w.rule)
		}(w)
	}

	return func() error {
		// Cancel subs then wait workers
		b.stopAll()
		wg.Wait()
		return nil
	}, nil
}

func (b *WebhookBridge) stopAll() {
	b.mu.Lock()
	subs := b.subs
	b.subs = nil
	b.mu.Unlock()

	for _, s := range subs {
		s.Cancel()
	}
}

func (b *WebhookBridge) runWorker(ctx context.Context, sub pubsub.Subscription, rule Rule) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sub.C():
			if !ok {
				return
			}
			b.handleMsg(ctx, msg, rule)
		}
	}
}

func (b *WebhookBridge) handleMsg(ctx context.Context, msg pubsub.Message, rule Rule) {
	// Build outbound webhook.Event.
	// NOTE: Data in pubsub is json.RawMessage in our WebhookInHandler.
	raw, _ := msg.Data.(json.RawMessage)
	if len(raw) == 0 {
		// If no raw payload, still emit a minimal event with meta only.
		raw = json.RawMessage([]byte(`{}`))
	}

	// Map to webhook.Event
	ev := Event{
		Type: rule.OutEventType,
		// OccurredAt will be set inside TriggerEvent; keep inbound time in meta for traceability.
		Data: map[string]any{
			"payload": raw,
		},
		Meta: map[string]any{
			"source":      firstNonEmpty(rule.Source, msg.Meta["source"]),
			"in_topic":    msg.Topic,
			"in_id":       msg.ID,
			"in_time":     msg.Time.Format(time.RFC3339Nano),
			"request_id":  msg.Meta["request_id"],
			"delivery_id": msg.Meta["delivery_id"], // github delivery / stripe event id
			"event_type":  msg.Meta["event_type"],
			"client_ip":   msg.Meta["client_ip"],
		},
	}

	// Fire-and-forget trigger. It will enqueue deliveries to outbound targets.
	_, _ = b.Out.TriggerEvent(ctx, ev)
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}
