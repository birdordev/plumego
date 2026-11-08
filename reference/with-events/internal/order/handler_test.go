package order

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestCreateOrderReturnsAccepted(t *testing.T) {
	bus := pubsub.New()
	defer bus.Close()
	handler := NewHandler(NewPublisher(bus, NewMemoryIdempotencyStore()), discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"id":"ord-1","customer_id":"cust-1","total_cents":1200}`))
	rec := httptest.NewRecorder()

	handler.Create(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

func TestCreateOrderEmptyBodyReturnsBadRequest(t *testing.T) {
	bus := pubsub.New()
	defer bus.Close()
	handler := NewHandler(NewPublisher(bus, NewMemoryIdempotencyStore()), discardLogger())

	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(""))
	rec := httptest.NewRecorder()

	handler.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestConsumerSkipsDuplicateMessage(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	store.Mark("msg-1")
	consumer := NewConsumer(nil, store)

	processed := consumer.ProcessMessage(t.Context(), pubsub.Message{ID: "msg-1"})

	if processed {
		t.Fatal("expected duplicate message to be skipped")
	}
	if consumer.Processed() != 0 {
		t.Fatalf("processed count = %d, want 0", consumer.Processed())
	}
}

func TestConsumerProcessesNewMessage(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	consumer := NewConsumer(nil, store)

	processed := consumer.ProcessMessage(t.Context(), pubsub.Message{ID: "msg-1"})

	if !processed {
		t.Fatal("expected new message to be processed")
	}
	if !store.Seen("msg-1") {
		t.Fatal("expected message to be marked processed")
	}
	if consumer.Processed() != 1 {
		t.Fatalf("processed count = %d, want 1", consumer.Processed())
	}
}
