// Package instrumentation provides observability wrappers for AI components.
// These wrappers add metrics collection without modifying the original components.
package instrumentation

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/metrics"
	"github.com/birdordev/plumego/x/ai/provider"
)

// InstrumentedProvider wraps a Provider with metrics collection.
type InstrumentedProvider struct {
	provider  provider.Provider
	collector metrics.Collector
}

// NewInstrumentedProvider creates a new instrumented provider.
func NewInstrumentedProvider(p provider.Provider, collector metrics.Collector) *InstrumentedProvider {
	return &InstrumentedProvider{
		provider:  p,
		collector: collector,
	}
}

// Name implements provider.Provider
func (ip *InstrumentedProvider) Name() string {
	return ip.provider.Name()
}

// Complete implements provider.Provider with metrics collection
func (ip *InstrumentedProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	start := time.Now()

	// Common tags
	tags, _ := metrics.Tags(
		"provider", ip.provider.Name(),
		"model", req.Model,
	)

	// Increment request counter
	ip.collector.Counter("ai_requests_total", 1, tags...)

	// Execute request
	resp, err := ip.provider.Complete(ctx, req)

	// Record duration
	duration := time.Since(start)
	ip.collector.Timing("ai_request_duration_seconds", duration, tags...)

	// Record status
	status := "success"
	if err != nil {
		status = "error"
		ip.collector.Counter("ai_request_errors_total", 1, append(tags, metrics.Tag{Key: "error_type", Value: "request_failed"})...)
	}
	statusTags := append(tags, metrics.Tag{Key: "status", Value: status})
	ip.collector.Counter("ai_requests_by_status_total", 1, statusTags...)

	// Record token usage
	if resp != nil {
		inputTokenTags := append(tags, metrics.Tag{Key: "type", Value: "input"})
		outputTokenTags := append(tags, metrics.Tag{Key: "type", Value: "output"})

		ip.collector.Counter("ai_request_tokens_total", float64(resp.Usage.InputTokens), inputTokenTags...)
		ip.collector.Counter("ai_request_tokens_total", float64(resp.Usage.OutputTokens), outputTokenTags...)
		ip.collector.Counter("ai_request_tokens_total", float64(resp.Usage.TotalTokens),
			append(tags, metrics.Tag{Key: "type", Value: "total"})...)

		// Record tool usage
		if resp.HasToolUse() {
			toolUses := resp.GetToolUses()
			ip.collector.Counter("ai_tool_uses_total", float64(len(toolUses)),
				append(tags, metrics.Tag{Key: "count", Value: string(rune(len(toolUses)))})...)
		}
	}

	return resp, err
}

// CompleteStream implements provider.Provider
func (ip *InstrumentedProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	start := time.Now()

	tags, _ := metrics.Tags(
		"provider", ip.provider.Name(),
		"model", req.Model,
	)

	// Increment streaming request counter
	ip.collector.Counter("ai_stream_requests_total", 1, tags...)

	reader, err := ip.provider.CompleteStream(ctx, req)

	// Record duration to establish stream
	duration := time.Since(start)
	ip.collector.Timing("ai_stream_setup_duration_seconds", duration, tags...)

	if err != nil {
		ip.collector.Counter("ai_stream_errors_total", 1, append(tags, metrics.Tag{Key: "error_type", Value: "setup_failed"})...)
	}

	return reader, err
}

// ListModels implements provider.Provider
func (ip *InstrumentedProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	start := time.Now()

	models, err := ip.provider.ListModels(ctx)

	duration := time.Since(start)
	tags, _ := metrics.Tags("provider", ip.provider.Name())
	ip.collector.Timing("ai_list_models_duration_seconds", duration, tags...)

	if err == nil {
		ip.collector.Gauge("ai_available_models_count", float64(len(models)), tags...)
	}

	return models, err
}

// GetModel implements provider.Provider
func (ip *InstrumentedProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	start := time.Now()

	model, err := ip.provider.GetModel(ctx, modelID)

	duration := time.Since(start)
	tags, _ := metrics.Tags(
		"provider", ip.provider.Name(),
		"model", modelID,
	)
	ip.collector.Timing("ai_get_model_duration_seconds", duration, tags...)

	return model, err
}

// CountTokens implements provider.Provider
func (ip *InstrumentedProvider) CountTokens(text string) (int, error) {
	start := time.Now()

	count, err := ip.provider.CountTokens(text)

	duration := time.Since(start)
	tags, _ := metrics.Tags("provider", ip.provider.Name())
	ip.collector.Timing("ai_count_tokens_duration_seconds", duration, tags...)

	if err == nil {
		ip.collector.Histogram("ai_token_count_value", float64(count), tags...)
	}

	return count, err
}
