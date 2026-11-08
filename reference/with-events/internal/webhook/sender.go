// Package webhook delivers order events to an optional external webhook target.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	xwebhook "github.com/birdordev/plumego/x/messaging/webhook"
	"with-events/internal/order"
)

const (
	MaxRetries = 3
	baseDelay  = time.Second
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Option func(*Sender)

type Sender struct {
	client    HTTPDoer
	targetURL string
	sleep     func(context.Context, time.Duration) error
}

func NewSender(targetURL string, opts ...Option) *Sender {
	s := &Sender{
		client:    http.DefaultClient,
		targetURL: strings.TrimSpace(targetURL),
		sleep:     sleepContext,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithHTTPClient(client HTTPDoer) Option {
	return func(s *Sender) {
		if client != nil {
			s.client = client
		}
	}
}

func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(s *Sender) {
		if sleep != nil {
			s.sleep = sleep
		}
	}
}

func (s *Sender) Send(ctx context.Context, event order.OrderCreated) error {
	if s == nil || strings.TrimSpace(s.targetURL) == "" {
		return nil
	}
	if s.client == nil {
		return fmt.Errorf("webhook sender is not configured")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order event: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= MaxRetries; attempt++ {
		status, err := s.sendOnce(ctx, raw)
		if err == nil && status >= http.StatusOK && status <= 299 {
			return nil
		}
		lastErr = deliveryError(status, err)
		if !xwebhook.ShouldRetry(status, err, attempt, MaxRetries, true) {
			return lastErr
		}
		if err := s.sleep(ctx, backoff(attempt)); err != nil {
			return fmt.Errorf("wait before webhook retry: %w", err)
		}
	}
	return lastErr
}

func (s *Sender) sendOnce(ctx context.Context, raw []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.targetURL, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func deliveryError(status int, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("webhook delivery failed with status %d", status)
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := baseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
