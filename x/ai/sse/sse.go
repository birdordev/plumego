// Package sse provides Server-Sent Events (SSE) support for streaming AI responses.
package sse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/spcent/plumego/contract"
)

const defaultErrorEventData = "stream handler failed"

// Event represents a single SSE event.
type Event struct {
	// Event type (optional)
	Event string
	// Event data (required)
	Data string
	// Event ID for client resumption (optional)
	ID string
	// Retry interval in milliseconds (optional)
	Retry int
	// Comment line (optional, for keep-alive)
	Comment string
}

// Stream represents an active SSE connection.
type Stream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ctx     context.Context
	mu      sync.Mutex
	closed  bool

	// Configuration
	keepAliveInterval time.Duration
	keepAliveTicker   *time.Ticker
	keepAliveStop     chan struct{}
	done              chan struct{}
}

// NewStream creates a new SSE stream.
func NewStream(ctx context.Context, w http.ResponseWriter) (*Stream, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	stream := &Stream{
		w:                 w,
		flusher:           flusher,
		ctx:               ctx,
		keepAliveInterval: 15 * time.Second,
		done:              make(chan struct{}),
	}

	// Start keep-alive
	stream.startKeepAlive()

	return stream, nil
}

// Send sends an event to the client.
func (s *Stream) Send(event *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("stream closed")
	}

	// Check context
	if err := s.ctx.Err(); err != nil {
		return err
	}

	// Write event
	if err := s.writeEvent(event); err != nil {
		return err
	}

	s.flusher.Flush()
	return nil
}

// SendData is a convenience method to send data without event type.
func (s *Stream) SendData(data string) error {
	return s.Send(&Event{Data: data})
}

// SendJSON sends JSON data as an event.
func (s *Stream) SendJSON(eventType string, jsonData string) error {
	return s.Send(&Event{
		Event: eventType,
		Data:  jsonData,
	})
}

// SendComment sends a comment (for keep-alive).
func (s *Stream) SendComment(comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	_, err := fmt.Fprintf(s.w, ": %s\n\n", comment)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// Close closes the stream.
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	close(s.done)

	if s.keepAliveTicker != nil {
		s.keepAliveTicker.Stop()
		s.keepAliveTicker = nil
	}
	if s.keepAliveStop != nil {
		close(s.keepAliveStop)
		s.keepAliveStop = nil
	}

	return nil
}

// SetKeepAliveInterval sets the keep-alive interval.
func (s *Stream) SetKeepAliveInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.keepAliveInterval = interval
	if s.keepAliveTicker != nil {
		s.keepAliveTicker.Stop()
		s.keepAliveTicker = nil
	}
	if s.keepAliveStop != nil {
		close(s.keepAliveStop)
		s.keepAliveStop = nil
	}
	if interval > 0 && !s.closed {
		ticker := time.NewTicker(interval)
		stop := make(chan struct{})
		s.keepAliveTicker = ticker
		s.keepAliveStop = stop
		go s.runKeepAlive(ticker, stop)
	}
}

// writeEvent writes an event to the response writer.
func (s *Stream) writeEvent(event *Event) error {
	// Write comment
	if event.Comment != "" {
		if _, err := fmt.Fprintf(s.w, ": %s\n", event.Comment); err != nil {
			return err
		}
	}

	// Write ID
	if event.ID != "" {
		if _, err := fmt.Fprintf(s.w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}

	// Write event type
	if event.Event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event.Event); err != nil {
			return err
		}
	}

	// Write retry
	if event.Retry > 0 {
		if _, err := fmt.Fprintf(s.w, "retry: %d\n", event.Retry); err != nil {
			return err
		}
	}

	// Write data (can be multi-line)
	if event.Data != "" {
		if _, err := fmt.Fprintf(s.w, "data: %s\n", event.Data); err != nil {
			return err
		}
	}

	// End of event
	if _, err := fmt.Fprint(s.w, "\n"); err != nil {
		return err
	}

	return nil
}

// startKeepAlive starts the keep-alive ticker.
func (s *Stream) startKeepAlive() {
	if s.keepAliveInterval <= 0 {
		return
	}

	ticker := time.NewTicker(s.keepAliveInterval)
	stop := make(chan struct{})
	s.keepAliveTicker = ticker
	s.keepAliveStop = stop
	go s.runKeepAlive(ticker, stop)
}

// runKeepAlive sends keep-alive comments.
func (s *Stream) runKeepAlive(ticker *time.Ticker, stop <-chan struct{}) {
	for {
		select {
		case <-ticker.C:
			if err := s.SendComment("keep-alive"); err != nil {
				return
			}
		case <-stop:
			return
		case <-s.done:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// Handler wraps an SSE handler function.
type Handler func(*Stream) error

// Handle creates an http.HandlerFunc from an SSE handler.
func Handle(handler Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Create stream
		stream, err := NewStream(r.Context(), w)
		if err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInternal).
				Message("failed to create SSE stream").
				Build())
			return
		}
		defer func() { _ = stream.Close() }()

		// Call handler
		if err := handler(stream); err != nil {
			// If error is context cancellation, it's expected
			if err != context.Canceled && err != context.DeadlineExceeded && err != io.EOF {
				// Try to send error event
				_ = stream.Send(&Event{
					Event: "error",
					Data:  defaultErrorEventData,
				})
			}
		}
	}
}
