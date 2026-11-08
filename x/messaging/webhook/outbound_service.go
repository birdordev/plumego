package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics tracks webhook delivery statistics.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	service := webhookout.NewService(store, config)
//	metrics := service.Metrics()
//	fmt.Printf("Enqueued: %d, SentOK: %d\n", metrics.Enqueued, metrics.SentOK)
type Metrics struct {
	// Enqueued is the number of deliveries enqueued
	Enqueued uint64

	// Dropped is the number of deliveries dropped due to queue overflow
	Dropped uint64

	// SentOK is the number of deliveries sent successfully
	SentOK uint64

	// SentFail is the number of deliveries that failed
	SentFail uint64

	// Retried is the number of deliveries that were retried
	Retried uint64

	// Dead is the number of deliveries that are dead (too many failures)
	Dead uint64
}

// Service manages webhook delivery with retry logic and queue management.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	store := webhookout.NewMemoryStore()
//	config := webhookout.DefaultConfig()
//	service := webhookout.NewService(store, config)
//	defer service.Stop()
//
//	// Start the service
//	go service.Start(context.Background())
//
//	// Trigger an event
//	event := webhookout.Event{
//		Type: "payment.success",
//		Data: map[string]any{"amount": 100.00},
//	}
//	count, err := service.TriggerEvent(context.Background(), event)
type Service struct {
	cfg   Config
	store Store

	queue *Queue
	ds    *DelayScheduler

	http *http.Client

	wg      sync.WaitGroup
	cancel  context.CancelFunc
	running atomic.Bool

	metrics Metrics
}

// NewService creates a new webhook delivery service.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	store := webhookout.NewMemoryStore()
//	config := webhookout.DefaultConfig()
//	service := webhookout.NewService(store, config)
func NewService(store Store, cfg Config) *Service {
	q := NewQueue(cfg.QueueSize, cfg.DropPolicy, cfg.BlockWait)
	s := &Service{
		cfg:   cfg,
		store: store,
		queue: q,
		http:  NewHTTPClient(cfg.DefaultTimeout),
	}
	s.ds = NewDelayScheduler(q)
	return s
}

// Start starts the webhook delivery service.
//
// This method:
//   - Starts the delay scheduler for retry logic
//   - Starts worker goroutines for processing deliveries
//   - Runs until the context is cancelled
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	store := webhookout.NewMemoryStore()
//	config := webhookout.DefaultConfig()
//	service := webhookout.NewService(store, config)
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	go service.Start(ctx)
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		return
	}
	if s.running.Swap(true) {
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// delay scheduler
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.ds.Run(cctx)
	}()

	// workers
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.workerLoop(cctx)
		}()
	}
}

// Stop gracefully stops the webhook delivery service.
//
// This method:
//   - Cancels all worker goroutines
//   - Waits for the queue to drain (up to DrainMax)
//   - Stops the delay scheduler
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	store := webhookout.NewMemoryStore()
//	config := webhookout.DefaultConfig()
//	service := webhookout.NewService(store, config)
//
//	ctx, cancel := context.WithCancel(context.Background())
//	go service.Start(ctx)
//
//	// ... use service ...
//
//	service.Stop()
func (s *Service) Stop() {
	if !s.running.Swap(false) {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.ds.Stop()

	// best-effort drain: wait up to DrainMax
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(s.cfg.DrainMax)
	select {
	case <-done:
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
	}
}

// Metrics returns the current delivery metrics.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	store := webhookout.NewMemoryStore()
//	config := webhookout.DefaultConfig()
//	service := webhookout.NewService(store, config)
//
//	metrics := service.Metrics()
//	fmt.Printf("Enqueued: %d, SentOK: %d\n", metrics.Enqueued, metrics.SentOK)
func (s *Service) Metrics() Metrics { return s.metrics }

// CreateTarget creates a new webhook target.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	target := webhookout.Target{
//		Name:    "Payment Service",
//		URL:     "https://api.example.com/webhooks",
//		Secret:  "secret-key-here",
//		Events:  []string{"payment.success", "payment.failed"},
//		Enabled: true,
//	}
//	created, err := service.CreateTarget(context.Background(), target)
func (s *Service) CreateTarget(ctx context.Context, t Target) (Target, error) {
	if err := validateTarget(t); err != nil {
		return Target{}, err
	}
	t.ID = newID()
	if t.Headers == nil {
		t.Headers = map[string]string{}
	}
	// defaults
	if t.TimeoutMs == 0 {
		t.TimeoutMs = int(s.cfg.DefaultTimeout / time.Millisecond)
	}
	if t.MaxRetries == 0 {
		t.MaxRetries = s.cfg.DefaultMaxRetries
	}
	if t.BackoffBaseMs == 0 {
		t.BackoffBaseMs = int(s.cfg.BackoffBase / time.Millisecond)
	}
	if t.BackoffMaxMs == 0 {
		t.BackoffMaxMs = int(s.cfg.BackoffMax / time.Millisecond)
	}
	return s.store.CreateTarget(ctx, t)
}

// UpdateTarget updates an existing webhook target.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	enabled := false
//	patch := webhookout.TargetPatch{
//		Enabled: &enabled,
//	}
//	updated, err := service.UpdateTarget(context.Background(), "target-123", patch)
func (s *Service) UpdateTarget(ctx context.Context, id string, patch TargetPatch) (Target, error) {
	// validate URL if set
	if patch.URL != nil {
		if err := validateURL(ctx, *patch.URL, s.cfg.AllowPrivateNetwork); err != nil {
			return Target{}, err
		}
	}
	if patch.Events != nil && len(*patch.Events) == 0 {
		return Target{}, errors.New("events cannot be empty")
	}
	if patch.Secret != nil && len(strings.TrimSpace(*patch.Secret)) < 8 {
		return Target{}, errors.New("secret too short")
	}
	return s.store.UpdateTarget(ctx, id, patch)
}

// GetTarget retrieves a webhook target by ID.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	target, ok := service.GetTarget(context.Background(), "target-123")
//	if !ok {
//		// Target not found
//	}
func (s *Service) GetTarget(ctx context.Context, id string) (Target, bool) {
	return s.store.GetTarget(ctx, id)
}

// ListTargets lists webhook targets matching the filter.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	enabled := true
//	filter := webhookout.TargetFilter{
//		Enabled: &enabled,
//		Event:   "payment.success",
//	}
//	targets, err := service.ListTargets(context.Background(), filter)
func (s *Service) ListTargets(ctx context.Context, f TargetFilter) ([]Target, error) {
	return s.store.ListTargets(ctx, f)
}

// GetDelivery retrieves a webhook delivery by ID.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	delivery, ok := service.GetDelivery(context.Background(), "del-123")
//	if !ok {
//		// Delivery not found
//	}
func (s *Service) GetDelivery(ctx context.Context, id string) (Delivery, bool) {
	return s.store.GetDelivery(ctx, id)
}

// ListDeliveries lists webhook deliveries matching the filter.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	filter := webhookout.DeliveryFilter{
//		Limit: 100,
//	}
//	deliveries, err := service.ListDeliveries(context.Background(), filter)
func (s *Service) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]Delivery, error) {
	return s.store.ListDeliveries(ctx, f)
}

// ReplayDelivery replays a failed delivery by creating a new delivery with the same payload.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	newDelivery, err := service.ReplayDelivery(context.Background(), "del-123")
//	if err != nil {
//		// Handle error
//	}
func (s *Service) ReplayDelivery(ctx context.Context, deliveryID string) (Delivery, error) {
	d, ok := s.store.GetDelivery(ctx, deliveryID)
	if !ok {
		return Delivery{}, ErrTargetNotFound
	}
	if len(d.PayloadJSON) == 0 {
		return Delivery{}, errors.New("delivery has no payload_json")
	}

	newID := newID()

	// Update delivery_id in payload to maintain idempotency
	// This is critical for replayed deliveries to avoid duplicate processing
	raw := d.PayloadJSON
	raw2, err := rewriteDeliveryIDInPayload(raw, newID)
	if err != nil {
		// JSON rewrite failed - this could break idempotency
		// Return error instead of silently using corrupted payload
		return Delivery{}, fmt.Errorf("failed to update delivery_id in payload: %w", err)
	}

	newD := Delivery{
		ID:          newID,
		TargetID:    d.TargetID,
		EventID:     d.EventID,
		EventType:   d.EventType,
		PayloadJSON: raw2,
		Attempt:     0,
		Status:      DeliveryPending,
	}
	newD, err = s.store.CreateDelivery(ctx, newD)
	if err != nil {
		return Delivery{}, err
	}
	if err := s.enqueue(ctx, Task{DeliveryID: newD.ID}); err != nil {
		return Delivery{}, err
	}
	return newD, nil
}

// rewriteDeliveryIDInPayload updates the delivery_id in the payload metadata.
func rewriteDeliveryIDInPayload(raw []byte, newDeliveryID string) ([]byte, error) {
	// payload schema:
	// { id, type, occurred_at, data, meta: { delivery_id, ... } }
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	meta, ok := m["meta"].(map[string]any)
	if !ok || meta == nil {
		meta = map[string]any{}
	}
	meta["delivery_id"] = newDeliveryID
	m["meta"] = meta
	return json.Marshal(m)
}

// TriggerEvent triggers a webhook event to all subscribed targets.
//
// This is the main method for sending webhooks. It:
//   - Creates an event with a unique ID
//   - Finds all enabled targets subscribed to this event type
//   - Creates a delivery for each target
//   - Enqueues the delivery for processing
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	event := webhookout.Event{
//		Type: "payment.success",
//		Data: map[string]any{
//			"amount":     100.00,
//			"currency":   "USD",
//			"customer_id": "cust-456",
//		},
//	}
//	count, err := service.TriggerEvent(context.Background(), event)
//	if err != nil {
//		// Handle error
//	}
//	fmt.Printf("Delivered to %d targets\n", count)
func (s *Service) TriggerEvent(ctx context.Context, e Event) (int, error) {
	if strings.TrimSpace(e.Type) == "" {
		return 0, errors.New("event type required")
	}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	if e.Meta == nil {
		e.Meta = map[string]any{}
	}

	e.ID = newID()
	e.OccurredAt = time.Now().UTC()

	targets, err := s.store.ListTargets(ctx, TargetFilter{Enabled: boolPtr(true), Event: e.Type})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, t := range targets {
		// Delivery ID first, so payload can include it
		deliveryID := newID()

		payload := map[string]any{
			"id":          e.ID,
			"type":        e.Type,
			"occurred_at": e.OccurredAt.Format(time.RFC3339Nano),
			"data":        e.Data,
			"meta": mergeMeta(e.Meta, map[string]any{
				"delivery_id": deliveryID,
				"target_id":   t.ID,
			}),
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			// Event data contains a non-serialisable value; skip this target
			// rather than sending a corrupt or empty payload.
			atomic.AddUint64(&s.metrics.SentFail, 1)
			continue
		}

		d := Delivery{
			ID:          deliveryID,
			TargetID:    t.ID,
			EventID:     e.ID,
			EventType:   e.Type,
			PayloadJSON: raw,
			Attempt:     0,
			Status:      DeliveryPending,
		}

		if _, err := s.store.CreateDelivery(ctx, d); err != nil {
			// Delivery record could not be persisted; skip this target so we
			// don't enqueue a task that can never be looked up.
			atomic.AddUint64(&s.metrics.SentFail, 1)
			continue
		}
		if err := s.enqueue(ctx, Task{DeliveryID: d.ID}); err != nil {
			atomic.AddUint64(&s.metrics.Dropped, 1)
			continue
		}
		count++
	}
	return count, nil
}

// mergeMeta merges two metadata maps.
func mergeMeta(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// validateTarget validates a webhook target configuration.
func validateTarget(t Target) error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("name required")
	}
	if len(strings.TrimSpace(t.Secret)) < 8 {
		return errors.New("secret too short")
	}
	if len(t.Events) == 0 {
		return errors.New("events required")
	}
	return validateURL(context.Background(), t.URL, false) // strict by default; allow override at service-level update
}

// dnsLookupTimeout is the maximum time spent resolving a hostname during SSRF
// validation. Keeping it short prevents slow DNS from blocking worker goroutines.
const dnsLookupTimeout = 5 * time.Second

// validateURL validates a webhook URL and checks for private network addresses.
// A context is accepted so the caller can cancel or impose an outer deadline;
// DNS resolution additionally uses its own short timeout to bound blocking time.
func validateURL(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url scheme must be http/https")
	}
	if u.Host == "" {
		return errors.New("url host required")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url hostname required")
	}
	if allowPrivate {
		return nil
	}

	// Resolve with a short timeout so a slow resolver cannot block workers.
	lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		// If DNS fails, reject to be safe (fail-closed).
		return errors.New("dns lookup failed")
	}
	for _, addr := range addrs {
		if isPrivateIP(addr.IP) {
			return errors.New("private network address not allowed")
		}
	}
	return nil
}

// isPrivateIP checks if an IP address is in a private range.
func isPrivateIP(ip net.IP) bool {
	// IPv4 private ranges + loopback + link-local
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	ip4 := ip.To4()
	if ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true
		case ip4[0] == 192 && ip4[1] == 168:
			return true
		case ip4[0] == 169 && ip4[1] == 254:
			return true
		}
		return false
	}
	// IPv6 unique local: fc00::/7
	if len(ip) == 16 && (ip[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

// ms converts milliseconds to duration, using default if value is <= 0.
func ms(v int, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return time.Duration(v) * time.Millisecond
}

// boolPtr returns a pointer to a bool value.
func boolPtr(v bool) *bool { return &v }

// newID generates a random ID.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
