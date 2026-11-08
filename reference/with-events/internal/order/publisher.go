package order

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

const CreatedTopic = "orders.created"

type OrderCreated struct {
	ID         string    `json:"id"`
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	TotalCents int64     `json:"total_cents"`
	CreatedAt  time.Time `json:"created_at"`
}

type IdempotencyStore interface {
	Seen(id string) bool
	Mark(id string) bool
}

type MemoryIdempotencyStore struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{seen: make(map[string]struct{})}
}

func (s *MemoryIdempotencyStore) Seen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[id]
	return ok
}

func (s *MemoryIdempotencyStore) Mark(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[id]; ok {
		return false
	}
	s.seen[id] = struct{}{}
	return true
}

type Publisher interface {
	Publish(topic string, msg pubsub.Message) error
}

type OrderPublisher struct {
	bus   Publisher
	store IdempotencyStore
}

func NewPublisher(bus Publisher, store IdempotencyStore) *OrderPublisher {
	return &OrderPublisher{bus: bus, store: store}
}

func (p *OrderPublisher) Publish(_ context.Context, event OrderCreated) error {
	if p == nil || p.bus == nil {
		return fmt.Errorf("order publisher is not configured")
	}
	if event.ID == "" {
		event.ID = event.OrderID
	}
	if event.ID == "" {
		return fmt.Errorf("order event id is required")
	}
	if p.store != nil && !p.store.Mark(event.ID) {
		return nil
	}
	return p.bus.Publish(CreatedTopic, pubsub.Message{
		ID:    event.ID,
		Topic: CreatedTopic,
		Type:  CreatedTopic,
		Data:  event,
		Meta:  map[string]string{"source": "with-events"},
	})
}
