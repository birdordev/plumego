package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// enqueue adds a task to the delivery queue.
func (s *Service) enqueue(ctx context.Context, t Task) error {
	if !s.cfg.Enabled {
		return errors.New("webhook disabled")
	}
	err := s.queue.Enqueue(ctx, t)
	if err == nil {
		atomic.AddUint64(&s.metrics.Enqueued, 1)
	}
	return err
}

// workerLoop processes tasks from the delivery queue.
func (s *Service) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-s.queue.Chan():
			if !ok {
				return
			}
			s.handleTask(ctx, t)
		}
	}
}

// handleTask processes a single delivery task.
func (s *Service) handleTask(ctx context.Context, t Task) {
	d, ok := s.store.GetDelivery(ctx, t.DeliveryID)
	if !ok {
		return
	}
	target, ok := s.store.GetTarget(ctx, d.TargetID)
	if !ok || !target.Enabled {
		return
	}

	// payload must exist for replayable delivery
	raw := d.PayloadJSON
	if len(raw) == 0 {
		// mark failed permanently (data corruption)
		attempt := d.Attempt + 1
		msg := "missing payload_json"
		st := DeliveryDead
		patch := DeliveryPatch{
			Attempt:   &attempt,
			Status:    &st,
			LastError: &msg,
		}
		_, _ = s.store.UpdateDelivery(ctx, d.ID, patch)
		atomic.AddUint64(&s.metrics.Dead, 1)
		return
	}

	attempt := d.Attempt + 1

	start := time.Now()
	status, snippet, err := s.sendOnce(ctx, target, d, raw, attempt)
	durMs := int(time.Since(start) / time.Millisecond)

	patch := DeliveryPatch{
		Attempt:        &attempt,
		LastHTTPStatus: &status,
		LastDurationMs: &durMs,
	}
	if snippet != "" {
		patch.LastRespSnippet = &snippet
	}
	if err != nil {
		msg := err.Error()
		patch.LastError = &msg
	}

	maxRetries := target.MaxRetries
	if maxRetries <= 0 {
		maxRetries = s.cfg.DefaultMaxRetries
	}
	retryOn429 := s.cfg.RetryOn429
	if target.RetryOn429 != nil {
		retryOn429 = *target.RetryOn429
	}

	if ShouldRetry(status, err, attempt, maxRetries, retryOn429) {
		atomic.AddUint64(&s.metrics.Retried, 1)
		nextAt := NextBackoff(
			time.Now().UTC(),
			attempt,
			ms(target.BackoffBaseMs, s.cfg.BackoffBase),
			ms(target.BackoffMaxMs, s.cfg.BackoffMax),
		)
		st := DeliveryRetry
		patch.Status = &st
		patch.NextAt = &nextAt
		_, _ = s.store.UpdateDelivery(ctx, d.ID, patch)
		s.ds.Schedule(nextAt, Task{DeliveryID: d.ID})
		return
	}

	if err == nil && status >= 200 && status <= 299 {
		atomic.AddUint64(&s.metrics.SentOK, 1)
		st := DeliverySuccess
		patch.Status = &st
		patch.NextAt = nil
		_, _ = s.store.UpdateDelivery(ctx, d.ID, patch)
		return
	}

	atomic.AddUint64(&s.metrics.SentFail, 1)
	st := DeliveryFailed
	if attempt >= maxRetries {
		st = DeliveryDead
		atomic.AddUint64(&s.metrics.Dead, 1)
	}
	patch.Status = &st
	patch.NextAt = nil
	_, _ = s.store.UpdateDelivery(ctx, d.ID, patch)
}

// sendOnce sends a single webhook delivery.
func (s *Service) sendOnce(ctx context.Context, target Target, d Delivery, raw []byte, attempt int) (int, string, error) {
	// SSRF check — DNS resolution is done with a short timeout so a slow or
	// unresponsive resolver cannot block the worker goroutine indefinitely.
	if err := validateURL(ctx, target.URL, s.cfg.AllowPrivateNetwork); err != nil {
		return 0, "", err
	}

	// Apply per-target request timeout via context rather than creating a new
	// http.Transport on every call (which would prevent TCP connection reuse).
	timeout := ms(target.TimeoutMs, s.cfg.DefaultTimeout)
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", target.URL, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	// custom headers
	for k, v := range target.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	// signature headers
	ts := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	sig := SignV1(target.Secret, ts, raw)
	req.Header.Set("X-Webhook-Id", target.ID)
	req.Header.Set("X-Webhook-Event", d.EventType)
	req.Header.Set("X-Webhook-Delivery", d.ID)
	req.Header.Set("X-Webhook-Timestamp", ts)
	req.Header.Set("X-Webhook-Signature", "v1="+sig)
	req.Header.Set("X-Webhook-Attempt", strconv.Itoa(attempt))

	resp, err := s.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	// read small snippet only
	const maxRead = 4096
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxRead))
	snippet := strings.TrimSpace(string(b))
	if len(snippet) > 1024 {
		snippet = snippet[:1024]
	}
	return resp.StatusCode, snippet, nil
}
