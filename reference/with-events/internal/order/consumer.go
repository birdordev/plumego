package order

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type EventSink interface {
	Send(context.Context, OrderCreated) error
}

type Subscriber interface {
	Subscribe(ctx context.Context, topic string, opts pubsub.SubOptions) (pubsub.Subscription, error)
}

type OrderConsumer struct {
	bus       Subscriber
	processed IdempotencyStore
	sink      EventSink
	count     atomic.Int64
}

func NewConsumer(bus Subscriber, processed IdempotencyStore, sinks ...EventSink) *OrderConsumer {
	var sink EventSink
	if len(sinks) > 0 {
		sink = sinks[0]
	}
	return &OrderConsumer{bus: bus, processed: processed, sink: sink}
}

func (c *OrderConsumer) Start(ctx context.Context) error {
	sub, err := c.bus.Subscribe(ctx, CreatedTopic, pubsub.SubOptions{BufferSize: 16})
	if err != nil {
		return err
	}
	go func() {
		defer sub.Cancel()
		for {
			select {
			case msg, ok := <-sub.C():
				if !ok {
					return
				}
				_ = c.ProcessMessage(ctx, msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (c *OrderConsumer) ProcessMessage(ctx context.Context, msg pubsub.Message) bool {
	if c == nil {
		return false
	}
	if c.processed != nil && c.processed.Seen(msg.ID) {
		return false
	}
	if c.sink != nil {
		event, err := orderCreatedFromMessage(msg)
		if err != nil {
			return false
		}
		if err := c.sink.Send(ctx, event); err != nil {
			return false
		}
	}
	if c.processed != nil {
		c.processed.Mark(msg.ID)
	}
	c.count.Add(1)
	return true
}

func (c *OrderConsumer) Processed() int64 {
	if c == nil {
		return 0
	}
	return c.count.Load()
}

func orderCreatedFromMessage(msg pubsub.Message) (OrderCreated, error) {
	switch data := msg.Data.(type) {
	case OrderCreated:
		return data, nil
	case *OrderCreated:
		if data == nil {
			return OrderCreated{}, fmt.Errorf("order event is nil")
		}
		return *data, nil
	default:
		raw, err := json.Marshal(data)
		if err != nil {
			return OrderCreated{}, fmt.Errorf("marshal order event: %w", err)
		}
		var event OrderCreated
		if err := json.Unmarshal(raw, &event); err != nil {
			return OrderCreated{}, fmt.Errorf("decode order event: %w", err)
		}
		if event.ID == "" {
			event.ID = msg.ID
		}
		return event, nil
	}
}
