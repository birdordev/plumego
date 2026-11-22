// Package sse provides Server-Sent Events (SSE) support for streaming AI responses.
package sse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
)

const defaultErrorEventData = "stream handler failed"

// Event represents a single SSE event.
type Event struct {
	// Event type (optional)
	Event string
	// Event data. LF, CRLF, and CR delimit lines within the same event.
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
	w            http.ResponseWriter
	flusher      http.Flusher
	ctx          context.Context
	mu           sync.Mutex
	closed       bool
	eventScratch []byte

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
	s.eventScratch = nil
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
	const smallEvent = 1024
	if len(event.Comment) <= smallEvent && len(event.ID) <= smallEvent && len(event.Event) <= smallEvent && len(event.Data) <= smallEvent &&
		len(event.Comment)+len(event.ID)+len(event.Event)+len(event.Data) <= smallEvent {
		buf := s.eventScratch[:0]
		if event.Comment != "" {
			buf = appendEventLine(buf, ": ", event.Comment)
		}
		if event.ID != "" {
			buf = appendEventLine(buf, "id: ", event.ID)
		}
		if event.Event != "" {
			buf = appendEventLine(buf, "event: ", event.Event)
		}
		if event.Retry > 0 {
			buf = append(buf, "retry: "...)
			buf = append(strconv.AppendInt(buf, int64(event.Retry), 10), '\n')
		}
		if event.Data != "" {
			data := event.Data
			for {
				i := strings.IndexAny(data, "\r\n")
				if i < 0 {
					buf = appendEventLine(buf, "data: ", data)
					break
				}
				buf = appendEventLine(buf, "data: ", data[:i])
				if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
					i++
				}
				data = data[i+1:]
			}
		}
		buf = append(buf, '\n')
		s.eventScratch = buf[:0]
		n, err := s.w.Write(buf)
		if err == nil && n != len(buf) {
			return io.ErrShortWrite
		}
		return err
	}
	return s.writeLargeEvent(event)
}

func appendEventLine(buf []byte, prefix, value string) []byte {
	buf = append(buf, prefix...)
	buf = append(buf, value...)
	return append(buf, '\n')
}

func (s *Stream) writeLargeEvent(event *Event) error {
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
		data := strings.ReplaceAll(event.Data, "\r\n", "\n")
		data = strings.ReplaceAll(data, "\r", "\n")
		for line := range strings.SplitSeq(data, "\n") {
			if _, err := fmt.Fprintf(s.w, "data: %s\n", line); err != nil {
				return err
			}
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

// Handle creates an http.HandlerFunc from an SSE handler. Cancellation,
// deadline expiry and EOF can be wrapped and still end the stream quietly.
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
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, io.EOF) {
				// Try to send error event
				_ = stream.Send(&Event{
					Event: "error",
					Data:  defaultErrorEventData,
				})
			}
		}
	}
}
