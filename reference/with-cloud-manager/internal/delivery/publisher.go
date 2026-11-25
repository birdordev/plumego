package delivery

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/x/messaging/webhook"
)

const sendTimeout = 8 * time.Second
const maxDelay = 10 * time.Minute

type Publisher struct {
	store   Store
	client  http.Client
	sinks   map[string]Destination
	sinkIDs []string
}

func NewPublisher(ctx context.Context, store Store, destinations []Destination, client *http.Client) (*Publisher, error) {
	if len(destinations) == 0 {
		return nil, ErrBinding
	}
	if client == nil {
		client = webhook.NewHTTPClient(sendTimeout)
	}
	p := &Publisher{store: store, client: *client, sinks: make(map[string]Destination, len(destinations))}
	p.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, d := range destinations {
		if _, duplicate := p.sinks[d.id]; duplicate {
			return nil, ErrBinding
		}
		if err := store.checkBinding(ctx, d); err != nil {
			return nil, err
		}
		p.sinks[d.id] = d
		p.sinkIDs = append(p.sinkIDs, d.id)
	}
	return p, nil
}

func (p *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := p.step(ctx)
		if err != nil && ctx.Err() == nil {
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	return nil
}

func (p *Publisher) step(ctx context.Context) (bool, error) {
	c, err := p.store.claim(ctx, p.sinkIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	destination := p.sinks[c.sink]
	if !c.matches(destination) {
		return true, ErrBinding
	}
	if c.exhausted {
		return true, nil
	}
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	result := p.send(sendCtx, destination, c)
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	_, err = p.store.settle(cleanup, c, result)
	return true, err
}

func (p *Publisher) send(ctx context.Context, d Destination, c claim) settlement {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(c.body))
	if err != nil {
		return settlement{state: "failed", reason: "invalid_destination"}
	}
	stamped := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Id", d.id)
	req.Header.Set("X-Webhook-Event", c.eventType)
	req.Header.Set("X-Webhook-Delivery", c.id)
	req.Header.Set("X-Webhook-Timestamp", stamped)
	req.Header.Set("X-Webhook-Signature", "v1="+webhook.SignV1(d.secret, stamped, c.body))
	req.Header.Set("X-Webhook-Attempt", strconv.Itoa(c.attempts))
	resp, err := p.client.Do(req)
	if err != nil {
		return failedDelivery(0, err, "", c.attempts)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return settlement{state: "delivered", httpStatus: resp.StatusCode}
	}
	return failedDelivery(resp.StatusCode, nil, resp.Header.Get("Retry-After"), c.attempts)
}

func failedDelivery(status int, err error, retryAfter string, attempts int) settlement {
	if status != 0 && (status < 100 || status > 599) {
		return settlement{state: "failed", reason: "invalid_http_status"}
	}
	reason := webhook.GetRetryReason(status, err, true)
	if reason == "unknown" {
		reason = "receiver_rejected"
	}
	if !webhook.ShouldRetry(status, err, attempts, maxAttempts, true) {
		return settlement{state: "failed", reason: reason, httpStatus: status}
	}
	now := time.Now()
	backoff := webhook.NextBackoff(now, attempts, time.Second, 2*time.Minute).Sub(now)
	return settlement{state: "retry", reason: reason, httpStatus: status, retryDelay: min(maxDelay, max(backoff, retryInterval(retryAfter, now)))}
}

func retryInterval(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return max(0, until.Sub(now))
	}
	return 0
}
