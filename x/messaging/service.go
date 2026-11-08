package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/metrics"
	"github.com/birdordev/plumego/security/input"
	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/messaging/scheduler"
	"github.com/birdordev/plumego/x/messaging/webhook"
)

// Service is the central message-sending coordinator.
// It validates requests, enqueues tasks into mq.TaskQueue, and starts
// workers that consume tasks and dispatch them via providers.
type Service struct {
	queue     *mq.TaskQueue
	worker    *mq.Worker
	store     mq.TaskStore
	scheduler *scheduler.Scheduler
	bus       *pubsub.InProcBroker
	templates *TemplateEngine
	logger    log.StructuredLogger

	sms   SMSProvider
	email EmailProvider

	receipts ReceiptStore
	quota    *QuotaChecker
	monitor  *ChannelMonitor
	metrics  *metricsWrapper
	webhook  *WebhookNotifier

	totalSent   atomic.Int64
	totalFailed atomic.Int64
}

// Config holds all dependencies required by the Service.
type Config struct {
	// TaskStore is the backing store for the task queue.
	// Use store.NewMemory for dev or store.NewSQL for prod.
	TaskStore mq.TaskStore

	// Scheduler is used for periodic jobs (DLQ retry, cleanup, etc.).
	Scheduler *scheduler.Scheduler

	// Bus is the in-process pub/sub for result events.
	Bus *pubsub.InProcBroker

	// SMS provider; nil disables SMS channel.
	SMS SMSProvider

	// Email provider; nil disables email channel.
	Email EmailProvider

	// Logger; optional.
	Logger log.StructuredLogger

	// ReceiptStore for persisting delivery receipts; nil uses in-memory default.
	Receipts ReceiptStore

	// QuotaChecker for per-tenant send limits; nil disables quota enforcement.
	Quota *QuotaChecker

	// Metrics for observability; nil disables metrics.
	// It records queue-level metrics and messaging-specific metric records.
	Metrics interface {
		mq.MetricsObserver
		metrics.Recorder
	}

	// Webhook is the outbound webhook service for delivery notifications.
	// When set together with Bus, a WebhookNotifier is created automatically.
	Webhook *webhook.Service

	// Worker tuning.
	WorkerConcurrency int
	WorkerMaxInflight int
	ConsumerID        string
}

// New creates a Service wired to the given dependencies.
func New(cfg Config) *Service {
	if cfg.Receipts == nil {
		cfg.Receipts = NewMemReceiptStore(0)
	}
	runtime := newServiceRuntime(cfg)

	monitor := NewChannelMonitor(cfg.SMS, cfg.Email, cfg.Logger)

	var wh *WebhookNotifier
	if cfg.Bus != nil && cfg.Webhook != nil {
		wh = NewWebhookNotifier(cfg.Bus, cfg.Webhook, cfg.Logger)
	}

	svc := &Service{
		queue:     runtime.queue,
		worker:    runtime.worker,
		store:     runtime.store,
		scheduler: cfg.Scheduler,
		bus:       cfg.Bus,
		templates: NewTemplateEngine(),
		logger:    cfg.Logger,
		sms:       cfg.SMS,
		email:     cfg.Email,
		receipts:  cfg.Receipts,
		quota:     cfg.Quota,
		monitor:   monitor,
		metrics:   newMetrics(cfg.Metrics),
		webhook:   wh,
	}

	if cfg.SMS != nil {
		runtime.worker.Register(topicFor(ChannelSMS), svc.handleSMS)
	}
	if cfg.Email != nil {
		runtime.worker.Register(topicFor(ChannelEmail), svc.handleEmail)
	}

	return svc
}

// Templates returns the template engine for registration.
func (s *Service) Templates() *TemplateEngine { return s.templates }

// Receipts returns the receipt store for direct queries.
func (s *Service) Receipts() ReceiptStore { return s.receipts }

// Monitor returns the channel health monitor.
func (s *Service) Monitor() *ChannelMonitor { return s.monitor }

// Start launches the worker pool, registers scheduled jobs,
// and starts the webhook notifier (if configured).
func (s *Service) Start(ctx context.Context) error {
	if err := s.registerScheduledJobs(); err != nil {
		return err
	}
	if s.monitor != nil {
		if err := s.monitor.RegisterJobs(s.scheduler); err != nil {
			return err
		}
	}
	s.worker.Start(ctx)
	if s.webhook != nil {
		if err := s.webhook.Start(ctx); err != nil && s.logger != nil {
			s.logger.Warn("webhook notifier start failed", log.Fields{"error": err.Error()})
		}
	}
	return nil
}

// Stop drains the worker pool and stops the webhook notifier.
func (s *Service) Stop(ctx context.Context) error {
	if s.webhook != nil {
		s.webhook.Stop()
	}
	return s.worker.Stop(ctx)
}

// Send validates, checks quota, and enqueues a single message.
func (s *Service) Send(ctx context.Context, req SendRequest) error {
	if err := s.validate(req); err != nil {
		if s.metrics.enabled() {
			s.metrics.ObserveValidation(ctx, req.Channel)
		}
		return err
	}
	if err := s.checkQuota(ctx, req); err != nil {
		return err
	}
	start := time.Now()
	err := s.enqueue(ctx, req)
	if s.metrics.enabled() {
		s.metrics.ObserveEnqueue(ctx, req.Channel, time.Since(start), err)
	}
	if err == nil {
		s.saveReceipt(req, messageStatusQueued, "", "")
	}
	return err
}

// SendBatch validates and enqueues multiple messages, returning a summary.
func (s *Service) SendBatch(ctx context.Context, batch BatchRequest) BatchResult {
	result := BatchResult{Total: len(batch.Requests)}
	for _, req := range batch.Requests {
		if err := s.Send(ctx, req); err != nil {
			id := strings.TrimSpace(req.ID)
			if id == "" {
				id = "<missing-id>"
			}
			result.Rejected++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		result.Accepted++
	}
	return result
}

// Stats returns current queue statistics.
func (s *Service) Stats(ctx context.Context) (ServiceStats, error) {
	qs, err := s.queue.Stats(ctx)
	if err != nil {
		return ServiceStats{}, err
	}
	return ServiceStats{
		Queued:      qs.Queued,
		InFlight:    qs.Leased,
		Dead:        qs.Dead,
		Expired:     qs.Expired,
		TotalSent:   s.totalSent.Load(),
		TotalFailed: s.totalFailed.Load(),
	}, nil
}

// --- internal ---

func (s *Service) checkQuota(ctx context.Context, req SendRequest) error {
	if s.quota == nil {
		return nil
	}
	return s.quota.Allow(ctx, req.TenantID)
}

func (s *Service) validate(req SendRequest) error {
	if strings.TrimSpace(req.ID) == "" {
		return ErrMissingID
	}
	if strings.TrimSpace(req.To) == "" {
		return ErrMissingRecipient
	}
	switch req.Channel {
	case ChannelSMS:
		if s.sms == nil {
			return fmt.Errorf("%w: sms provider not configured", ErrInvalidChannel)
		}
		if !input.ValidatePhone(req.To) {
			return ErrInvalidPhone
		}
		if req.Body == "" && req.Template == "" {
			return ErrMissingBody
		}
	case ChannelEmail:
		if s.email == nil {
			return fmt.Errorf("%w: email provider not configured", ErrInvalidChannel)
		}
		if !input.ValidateEmail(req.To) {
			return ErrInvalidEmail
		}
		if req.Subject == "" {
			return ErrMissingSubject
		}
		if req.Body == "" && req.Template == "" {
			return ErrMissingBody
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidChannel, req.Channel)
	}
	return nil
}

func (s *Service) enqueue(ctx context.Context, req SendRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("messaging: marshal request: %w", err)
	}

	pri := mq.MessagePriority(req.Priority)
	opts := mq.EnqueueOptions{
		DedupeKey:   req.DedupeKey,
		Priority:    &pri,
		MaxAttempts: req.MaxRetries,
	}
	if req.ScheduledAt != nil {
		opts.AvailableAt = *req.ScheduledAt
	}
	if req.TTL > 0 {
		opts.ExpiresAt = time.Now().Add(req.TTL)
	}

	task := mq.Task{
		ID:       req.ID,
		Topic:    topicFor(req.Channel),
		TenantID: req.TenantID,
		Payload:  payload,
	}
	return s.queue.Enqueue(ctx, task, opts)
}

func (s *Service) renderBody(req SendRequest) (string, error) {
	if req.Template != "" && s.templates.Has(req.Template) {
		return s.templates.Render(req.Template, req.Params)
	}
	if req.Body != "" {
		return req.Body, nil
	}
	return "", ErrMissingBody
}

// deliverTask is the unified handler for both SMS and email task processing.
// It unmarshals the request, renders the body, calls the channel-specific
// sendFn, and records metrics, receipts, and results.
func (s *Service) deliverTask(
	ctx context.Context,
	task mq.Task,
	channel Channel,
	providerName string,
	sendFn func(ctx context.Context, req SendRequest, body string) (providerID string, err error),
) error {
	var req SendRequest
	if err := json.Unmarshal(task.Payload, &req); err != nil {
		s.recordTaskFailure(ctx, task.ID, channel, providerName, err, task.Attempts, 0, false)
		return fmt.Errorf("messaging: unmarshal %s task: %w", channel, err)
	}
	reqID := strings.TrimSpace(req.ID)
	if reqID == "" {
		reqID = task.ID
	}

	body, err := s.renderBody(req)
	if err != nil {
		s.recordTaskFailure(ctx, reqID, channel, providerName, err, task.Attempts, 0, false)
		return err
	}

	start := time.Now()
	providerID, err := sendFn(ctx, req, body)
	elapsed := time.Since(start)

	if err != nil {
		s.recordTaskFailure(ctx, reqID, channel, providerName, err, task.Attempts, elapsed, true)
		return fmt.Errorf("%w: %w", ErrProviderFailure, err)
	}

	s.monitor.RecordSuccess(channel, elapsed)
	s.metrics.ObserveSend(ctx, channel, providerName, elapsed, nil)
	s.updateReceipt(reqID, messageStatusSent, providerID, providerName, "", task.Attempts)
	s.publishResult(SendResult{
		RequestID:  reqID,
		Channel:    channel,
		Status:     messageStatusSent,
		ProviderID: providerID,
		SentAt:     time.Now(),
		Attempts:   task.Attempts,
	})
	s.totalSent.Add(1)
	return nil
}

func (s *Service) handleSMS(ctx context.Context, task mq.Task) error {
	return s.deliverTask(ctx, task, ChannelSMS, s.sms.Name(),
		func(ctx context.Context, req SendRequest, body string) (string, error) {
			result, err := s.sms.Send(ctx, SMSMessage{To: req.To, Body: body})
			if err != nil {
				return "", err
			}
			return result.ProviderID, nil
		})
}

func (s *Service) handleEmail(ctx context.Context, task mq.Task) error {
	return s.deliverTask(ctx, task, ChannelEmail, s.email.Name(),
		func(ctx context.Context, req SendRequest, body string) (string, error) {
			result, err := s.email.Send(ctx, EmailMessage{
				To: req.To, Subject: req.Subject, Body: body, HTML: req.HTML,
			})
			if err != nil {
				return "", err
			}
			return result.MessageID, nil
		})
}

// providerNameFor returns the provider name for a channel.
func (s *Service) providerNameFor(ch Channel) string {
	switch ch {
	case ChannelSMS:
		if s.sms != nil {
			return s.sms.Name()
		}
	case ChannelEmail:
		if s.email != nil {
			return s.email.Name()
		}
	}
	return ""
}

func (s *Service) saveReceipt(req SendRequest, status, providerID, errMsg string) {
	if s.receipts == nil {
		return
	}
	_ = s.receipts.Save(Receipt{
		ID:         req.ID,
		Channel:    req.Channel,
		To:         req.To,
		Status:     status,
		ProviderID: providerID,
		Provider:   s.providerNameFor(req.Channel),
		Error:      errMsg,
		TenantID:   req.TenantID,
		QueuedAt:   time.Now(),
	})
}

func (s *Service) updateReceipt(id, status, providerID, provider, errMsg string, attempts int) {
	if s.receipts == nil || strings.TrimSpace(id) == "" {
		return
	}
	r, ok := s.receipts.Get(id)
	if !ok {
		return
	}
	r.Status = status
	r.Attempts = attempts
	r.Provider = provider
	if providerID != "" {
		r.ProviderID = providerID
	}
	if errMsg != "" {
		r.Error = errMsg
	}
	if status == messageStatusSent {
		r.SentAt = time.Now()
	}
	_ = s.receipts.Save(r)
}

func (s *Service) recordTaskFailure(
	ctx context.Context,
	requestID string,
	channel Channel,
	provider string,
	err error,
	attempts int,
	duration time.Duration,
	providerIssue bool,
) {
	if providerIssue && s.monitor != nil {
		s.monitor.RecordFailure(channel, err)
	}
	s.metrics.ObserveSend(ctx, channel, provider, duration, err)
	s.updateReceipt(requestID, messageStatusFailed, "", provider, err.Error(), attempts)
	s.publishResult(SendResult{
		RequestID: requestID,
		Channel:   channel,
		Status:    messageStatusFailed,
		Error:     err.Error(),
		Attempts:  attempts,
	})
	s.totalFailed.Add(1)
}

func (s *Service) publishResult(result SendResult) {
	if s.bus == nil {
		return
	}
	data, _ := json.Marshal(result)
	_ = s.bus.Publish("messaging.result", pubsub.Message{
		Topic: "messaging.result",
		Type:  string(result.Channel),
		Time:  time.Now(),
		Data:  string(data),
	})
}
