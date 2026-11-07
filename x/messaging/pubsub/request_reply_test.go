package pubsub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRequestReplyBasic tests basic request-reply using the request manager.
func TestRequestReplyBasic(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()

	sub, err := ps.Subscribe(t.Context(), "math.add", SubOptions{BufferSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	go func() {
		for msg := range sub.C() {
			if IsRequest(msg) {
				_ = Reply(ps, msg, Message{Data: "result:5"})
			}
		}
	}()

	resp, err := rm.RequestWithTimeout("math.add", Message{Data: "2+3"}, 2*time.Second)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if resp.Data != "result:5" {
		t.Fatalf("unexpected response data: %v", resp.Data)
	}
}

// TestRequestReplyWithContext tests request with context cancellation.
func TestRequestReplyWithContext(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err = rm.Request(ctx, "no.responder", Message{Data: "hello"})
	if err == nil {
		t.Fatal("expected error from context timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got: %v", err)
	}
}

// TestRequestReplyMultipleConcurrent tests concurrent requests.
func TestRequestReplyMultipleConcurrent(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()

	sub, err := ps.Subscribe(t.Context(), "echo", SubOptions{BufferSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	go func() {
		for msg := range sub.C() {
			if IsRequest(msg) {
				_ = Reply(ps, msg, Message{Data: msg.Data})
			}
		}
	}()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			resp, err := rm.RequestWithTimeout("echo", Message{Data: val}, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			if resp.Data != val {
				errs <- &Error{Code: ErrCodeInternal, Op: "test", Message: "response data mismatch"}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent request failed: %v", err)
	}
}

// TestRequestReplyCorrelationID tests that correlation IDs are correctly propagated.
func TestRequestReplyCorrelationID(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()

	sub, err := ps.Subscribe(t.Context(), "service.ping", SubOptions{BufferSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	var capturedCorrelationID string
	go func() {
		for msg := range sub.C() {
			if IsRequest(msg) {
				capturedCorrelationID = GetCorrelationID(msg)
				_ = Reply(ps, msg, Message{Data: "pong"})
			}
		}
	}()

	resp, err := rm.RequestWithTimeout("service.ping", Message{Data: "ping"}, 2*time.Second)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}

	respCorrelationID := GetCorrelationID(resp)
	if respCorrelationID == "" {
		t.Fatal("response missing correlation ID")
	}
	if capturedCorrelationID == "" {
		t.Fatal("request was missing correlation ID")
	}
	if respCorrelationID != capturedCorrelationID {
		t.Fatalf("correlation ID mismatch: request=%s response=%s",
			capturedCorrelationID, respCorrelationID)
	}
}

// TestRequestReplyOnClosedBroker tests that requests fail on closed broker.
func TestRequestReplyOnClosedBroker(t *testing.T) {
	ps := New()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	ps.Close()
	rm.Close()

	_, err = rm.Request(t.Context(), "test", Message{})
	if err == nil {
		t.Fatal("expected error on closed broker")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrCodeClosed {
		t.Fatalf("expected ErrCodeClosed, got: %v", err)
	}
}

// TestReplyNoReplyToHeader tests Reply with a non-request message.
func TestReplyNoReplyToHeader(t *testing.T) {
	ps := New()
	defer ps.Close()

	err := Reply(ps, Message{Topic: "test"}, Message{Data: "reply"})
	if err == nil {
		t.Fatal("expected error when replying to non-request message")
	}
}

// TestReplyNoCorrelationID tests Reply with missing correlation ID.
func TestReplyNoCorrelationID(t *testing.T) {
	ps := New()
	defer ps.Close()

	msg := Message{
		Topic: "test",
		Meta:  map[string]string{ReplyToHeader: "_reply.abc"},
	}
	err := Reply(ps, msg, Message{Data: "reply"})
	if err == nil {
		t.Fatal("expected error when replying without correlation ID")
	}
}

// TestIsRequest tests the IsRequest helper.
func TestIsRequest(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want bool
	}{
		{"nil meta", Message{}, false},
		{"empty meta", Message{Meta: map[string]string{}}, false},
		{"has reply-to", Message{Meta: map[string]string{ReplyToHeader: "_reply.abc"}}, true},
		{"has other meta", Message{Meta: map[string]string{"X-Custom": "val"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRequest(tt.msg)
			if got != tt.want {
				t.Fatalf("IsRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetCorrelationIDAndReplyTo tests the helper functions.
func TestGetCorrelationIDAndReplyTo(t *testing.T) {
	msg := Message{}
	if GetCorrelationID(msg) != "" {
		t.Fatal("expected empty correlation ID for nil meta")
	}
	if GetReplyTo(msg) != "" {
		t.Fatal("expected empty reply-to for nil meta")
	}

	msg.Meta = map[string]string{
		CorrelationIDHeader: "corr-123",
		ReplyToHeader:       "_reply.xyz",
	}
	if GetCorrelationID(msg) != "corr-123" {
		t.Fatalf("unexpected correlation ID: %s", GetCorrelationID(msg))
	}
	if GetReplyTo(msg) != "_reply.xyz" {
		t.Fatalf("unexpected reply-to: %s", GetReplyTo(msg))
	}
}

// TestRequestManagerClose tests that closing the request manager cleans up pending requests.
func TestRequestManagerClose(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}

	var requestErr atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := rm.RequestWithTimeout("no.responder", Message{Data: "test"}, 5*time.Second)
		if err != nil {
			requestErr.Store(err)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	rm.mu.RLock()
	n := len(rm.pending)
	rm.mu.RUnlock()
	if n != 1 {
		t.Fatalf("expected 1 pending request, got %d", n)
	}

	rm.Close()
	wg.Wait()
}

// TestRequestReplyManagerMultipleRequests tests multiple sequential requests.
func TestRequestReplyManagerMultipleRequests(t *testing.T) {
	ps := New()
	defer ps.Close()

	rm, err := newRequestManager(ps)
	if err != nil {
		t.Fatal(err)
	}
	defer rm.Close()

	sub, err := ps.Subscribe(t.Context(), "counter", SubOptions{BufferSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()

	var count atomic.Int32
	go func() {
		for msg := range sub.C() {
			if IsRequest(msg) {
				c := count.Add(1)
				_ = Reply(ps, msg, Message{Data: int(c)})
			}
		}
	}()

	for i := 1; i <= 5; i++ {
		resp, err := rm.RequestWithTimeout("counter", Message{Data: "inc"}, 2*time.Second)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if resp.Data != i {
			t.Fatalf("request %d: expected %d, got %v", i, i, resp.Data)
		}
	}
}
