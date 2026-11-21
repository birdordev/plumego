package instrumentation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/llmcache"
	"github.com/birdordev/plumego/x/ai/metrics"
	"github.com/birdordev/plumego/x/ai/orchestration"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// Mock provider for testing
type mockProvider struct {
	name      string
	response  string
	err       error
	callCount int
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return &provider.CompletionResponse{
		ID:    "test-id",
		Model: req.Model,
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.response},
		},
		Usage: tokenizer.TokenUsage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "test-model", Name: "Test Model", Provider: m.name}}, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return &provider.Model{ID: modelID, Name: "Test Model", Provider: m.name}, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text) / 4, nil
}

func TestInstrumentedProvider_Complete(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockProv := &mockProvider{name: "test-provider", response: "test response"}
	instrumented := NewInstrumentedProvider(mockProv, collector)

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test input"),
		},
	}

	resp, err := instrumented.Complete(t.Context(), req)

	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if resp.GetText() != "test response" {
		t.Errorf("Response text = %v, want 'test response'", resp.GetText())
	}

	// Check metrics
	snapshot := collector.Snapshot()

	// Check request counter (note: tags are ordered as provider,model in implementation)
	requestKey := "ai_requests_total{provider=test-provider,model=test-model}"
	if counter, exists := snapshot.Counters[requestKey]; !exists {
		t.Error("Request counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Request counter = %v, want 1.0", counter.Value)
	}

	// Check token counters
	inputTokenKey := "ai_request_tokens_total{provider=test-provider,model=test-model,type=input}"
	if counter, exists := snapshot.Counters[inputTokenKey]; !exists {
		t.Error("Input token counter not found")
	} else if counter.Value != 10.0 {
		t.Errorf("Input tokens = %v, want 10.0", counter.Value)
	}
}

func TestInstrumentedProvider_Complete_Error(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockProv := &mockProvider{
		name: "test-provider",
		err:  errors.New("test error"),
	}
	instrumented := NewInstrumentedProvider(mockProv, collector)

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	_, err := instrumented.Complete(t.Context(), req)

	if err == nil {
		t.Error("Complete() should return error")
	}

	// Check error counter
	snapshot := collector.Snapshot()
	errorKey := "ai_request_errors_total{provider=test-provider,model=test-model,error_type=request_failed}"
	if counter, exists := snapshot.Counters[errorKey]; !exists {
		t.Error("Error counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Error counter = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedProvider_CompleteStream(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockProv := &mockProvider{name: "test-provider"}
	instrumented := NewInstrumentedProvider(mockProv, collector)

	req := &provider.CompletionRequest{
		Model: "test-model",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "test"),
		},
	}

	_, err := instrumented.CompleteStream(t.Context(), req)

	if err != nil {
		t.Errorf("CompleteStream() error = %v", err)
	}

	// Check stream request counter
	snapshot := collector.Snapshot()
	streamKey := "ai_stream_requests_total{provider=test-provider,model=test-model}"
	if counter, exists := snapshot.Counters[streamKey]; !exists {
		t.Error("Stream request counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Stream request counter = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedProvider_ListModels(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockProv := &mockProvider{name: "test-provider"}
	instrumented := NewInstrumentedProvider(mockProv, collector)

	models, err := instrumented.ListModels(t.Context())

	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	if len(models) != 1 {
		t.Errorf("Models count = %v, want 1", len(models))
	}

	// Check gauge
	snapshot := collector.Snapshot()
	gaugeKey := "ai_available_models_count{provider=test-provider}"
	if gauge, exists := snapshot.Gauges[gaugeKey]; !exists {
		t.Error("Models count gauge not found")
	} else if gauge.Value != 1.0 {
		t.Errorf("Models count = %v, want 1.0", gauge.Value)
	}
}

// Mock cache for testing
type mockCache struct {
	entries map[string]*llmcache.CacheEntry
	getErr  error
	setErr  error
	hits    int64
	misses  int64
}

func newMockCache() *mockCache {
	return &mockCache{
		entries: make(map[string]*llmcache.CacheEntry),
	}
}

func (mc *mockCache) Get(ctx context.Context, key *llmcache.CacheKey) (*llmcache.CacheEntry, error) {
	if mc.getErr != nil {
		return nil, mc.getErr
	}
	entry, exists := mc.entries[key.Hash]
	if !exists {
		mc.misses++
		return nil, fmt.Errorf("cache miss")
	}
	mc.hits++
	return entry, nil
}

func (mc *mockCache) Set(ctx context.Context, key *llmcache.CacheKey, entry *llmcache.CacheEntry) error {
	if mc.setErr != nil {
		return mc.setErr
	}
	mc.entries[key.Hash] = entry
	return nil
}

func (mc *mockCache) Delete(ctx context.Context, key *llmcache.CacheKey) error {
	delete(mc.entries, key.Hash)
	return nil
}

func (mc *mockCache) Clear(ctx context.Context) error {
	mc.entries = make(map[string]*llmcache.CacheEntry)
	return nil
}

func (mc *mockCache) Stats() llmcache.CacheStats {
	return llmcache.CacheStats{
		Hits:   mc.hits,
		Misses: mc.misses,
	}
}

func TestInstrumentedCache_Get_Hit(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockC := newMockCache()

	// Pre-populate cache
	key := &llmcache.CacheKey{Hash: "test-hash"}
	entry := &llmcache.CacheEntry{
		Usage: tokenizer.TokenUsage{TotalTokens: 100},
	}
	mockC.Set(t.Context(), key, entry)

	instrumented := NewInstrumentedCache(mockC, collector, "test")

	result, err := instrumented.Get(t.Context(), key)

	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if result == nil {
		t.Fatal("Get() should return entry")
	}

	// Check hit counter
	snapshot := collector.Snapshot()
	hitKey := "ai_cache_hits_total{cache_type=test}"
	if counter, exists := snapshot.Counters[hitKey]; !exists {
		t.Error("Cache hit counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Cache hits = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedCache_Get_Miss(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockC := newMockCache()
	instrumented := NewInstrumentedCache(mockC, collector, "test")

	key := &llmcache.CacheKey{Hash: "nonexistent"}
	result, err := instrumented.Get(t.Context(), key)

	if err == nil || result != nil {
		t.Errorf("Get() should return error for cache miss, got result=%v err=%v", result, err)
	}

	// Check miss counter
	snapshot := collector.Snapshot()
	missKey := "ai_cache_misses_total{cache_type=test}"
	if counter, exists := snapshot.Counters[missKey]; !exists {
		t.Error("Cache miss counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Cache misses = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedCache_Set(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	mockC := newMockCache()
	instrumented := NewInstrumentedCache(mockC, collector, "test")

	key := &llmcache.CacheKey{Hash: "test-hash"}
	entry := &llmcache.CacheEntry{
		Usage: tokenizer.TokenUsage{TotalTokens: 50},
	}

	err := instrumented.Set(t.Context(), key, entry)

	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Check write counter
	snapshot := collector.Snapshot()
	writeKey := "ai_cache_writes_total{cache_type=test}"
	if counter, exists := snapshot.Counters[writeKey]; !exists {
		t.Error("Cache write counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Cache writes = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedMemoryCache_PublishStats(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	memCache := llmcache.NewMemoryCache(1*time.Hour, 100)
	instrumented := NewInstrumentedMemoryCache(memCache, collector)

	// Simulate some cache activity
	key := &llmcache.CacheKey{Hash: "test"}
	entry := &llmcache.CacheEntry{Usage: tokenizer.TokenUsage{TotalTokens: 100}}
	memCache.Set(t.Context(), key, entry)
	_, _ = memCache.Get(t.Context(), key)                              // Hit
	_, _ = memCache.Get(t.Context(), &llmcache.CacheKey{Hash: "miss"}) // Miss

	// Publish stats
	instrumented.PublishStats()

	// Check stats gauges
	snapshot := collector.Snapshot()

	hitsKey := "ai_cache_hits{cache_type=memory}"
	if gauge, exists := snapshot.Gauges[hitsKey]; !exists {
		t.Error("Hits gauge not found")
	} else if gauge.Value != 1.0 {
		t.Errorf("Hits gauge = %v, want 1.0", gauge.Value)
	}

	missesKey := "ai_cache_misses{cache_type=memory}"
	if gauge, exists := snapshot.Gauges[missesKey]; !exists {
		t.Error("Misses gauge not found")
	} else if gauge.Value != 1.0 {
		t.Errorf("Misses gauge = %v, want 1.0", gauge.Value)
	}
}

func TestInstrumentedEngine_Execute(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	engine := orchestration.NewEngine()

	// Create a simple workflow
	mockProv := &mockProvider{name: "test", response: "result"}
	agent := &orchestration.Agent{
		ID:       "agent-1",
		Name:     "Test Agent",
		Provider: mockProv,
		Model:    "test-model",
	}

	workflow := orchestration.NewWorkflow("wf-1", "Test Workflow", "Test")
	step := &orchestration.SequentialStep{
		StepName:  "step1",
		Agent:     agent,
		InputFn:   func(state map[string]any) string { return "input" },
		OutputKey: "output",
	}
	workflow.AddStep(step)

	instrumented := NewInstrumentedEngine(engine, collector)
	instrumented.RegisterWorkflow(workflow)

	results, err := instrumented.Execute(t.Context(), "wf-1", map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Results count = %v, want 1", len(results))
	}

	// Check metrics
	snapshot := collector.Snapshot()

	execKey := "ai_workflow_executions_total{workflow_id=wf-1}"
	if counter, exists := snapshot.Counters[execKey]; !exists {
		t.Error("Execution counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Executions = %v, want 1.0", counter.Value)
	}

	successKey := "ai_workflow_by_status_total{workflow_id=wf-1,status=success}"
	if counter, exists := snapshot.Counters[successKey]; !exists {
		t.Error("Success counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Successes = %v, want 1.0", counter.Value)
	}
}

func TestInstrumentedStep_Execute(t *testing.T) {
	collector := metrics.NewMemoryCollector()

	mockProv := &mockProvider{name: "test", response: "result"}
	agent := &orchestration.Agent{
		ID:       "agent-1",
		Name:     "Test Agent",
		Provider: mockProv,
		Model:    "test-model",
	}

	baseStep := &orchestration.SequentialStep{
		StepName:  "test-step",
		Agent:     agent,
		InputFn:   func(state map[string]any) string { return "input" },
		OutputKey: "output",
	}

	instrumented := NewInstrumentedStep(baseStep, collector)

	workflow := orchestration.NewWorkflow("wf-1", "Test", "Test")
	result, err := instrumented.Execute(t.Context(), workflow)

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Output != "result" {
		t.Errorf("Output = %v, want 'result'", result.Output)
	}

	// Check metrics
	snapshot := collector.Snapshot()

	stepExecKey := "ai_workflow_step_executions_total{workflow_id=wf-1,step_name=test-step}"
	if counter, exists := snapshot.Counters[stepExecKey]; !exists {
		t.Error("Step execution counter not found")
	} else if counter.Value != 1.0 {
		t.Errorf("Step executions = %v, want 1.0", counter.Value)
	}
}

type tokenCountingProvider struct {
	provider.Provider
	count func(string) (int, error)
}

func (p tokenCountingProvider) CountTokens(s string) (int, error) { return p.count(s) }

func TestInstrumentedCountTokensRecordsOnlySuccessfulCounts(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	sentinel := errors.New("test count failure")
	calls := 0
	p := tokenCountingProvider{Provider: &mockProvider{name: "fixture"}, count: func(s string) (int, error) {
		calls++
		switch s {
		case "success":
			return 7, nil
		case "failure":
			return 0, sentinel
		default:
			t.Errorf("unexpected input %q", s)
			return 0, nil
		}
	}}
	w := NewInstrumentedProvider(p, collector)
	if n, err := w.CountTokens("success"); n != 7 || err != nil {
		t.Errorf("successful count=%d, %v", n, err)
	}
	if _, err := w.CountTokens("failure"); !errors.Is(err, sentinel) {
		t.Errorf("count error=%v", err)
	}
	h := collector.Snapshot().Histograms
	count := h["ai_token_count_value{provider=fixture}"]
	timing := h["ai_count_tokens_duration_seconds{provider=fixture}"]
	if calls != 2 || timing.Count != 2 || count.Count != 1 || count.Sum != 7 {
		t.Errorf("calls=%d timing=%+v count=%+v", calls, timing, count)
	}
}

type mutationTestCache struct {
	llmcache.Cache
	delete func(context.Context, *llmcache.CacheKey) error
	clear  func(context.Context) error
}

func (c mutationTestCache) Delete(ctx context.Context, key *llmcache.CacheKey) error {
	return c.delete(ctx, key)
}
func (c mutationTestCache) Clear(ctx context.Context) error { return c.clear(ctx) }

func TestInstrumentedCacheMutationMetrics(t *testing.T) {
	ctx := t.Context()
	key := &llmcache.CacheKey{Hash: "fixture"}
	sentinel := errors.New("cache mutation failed")
	collector := metrics.NewMemoryCollector()
	deletes, clears := 0, 0
	var cause error
	cache := NewInstrumentedCache(mutationTestCache{
		delete: func(gotCtx context.Context, gotKey *llmcache.CacheKey) error {
			deletes++
			if gotCtx != ctx || gotKey != key {
				t.Error("Delete inputs changed")
			}
			return cause
		},
		clear: func(gotCtx context.Context) error {
			clears++
			if gotCtx != ctx {
				t.Error("Clear context changed")
			}
			return cause
		},
	}, collector, "fixture")

	for _, want := range []error{nil, sentinel} {
		cause = want
		if err := cache.Delete(ctx, key); !errors.Is(err, want) {
			t.Errorf("Delete = %v, want %v", err, want)
		}
		if err := cache.Clear(ctx); !errors.Is(err, want) {
			t.Errorf("Clear = %v, want %v", err, want)
		}
	}
	if deletes != 2 || clears != 2 {
		t.Errorf("delete calls = %d, clear calls = %d", deletes, clears)
	}
	snapshot := collector.Snapshot()
	for _, name := range []string{"ai_cache_delete_duration_seconds", "ai_cache_clear_duration_seconds"} {
		if got := snapshot.Histograms[name+"{cache_type=fixture}"].Count; got != 2 {
			t.Errorf("%s count = %d, want 2", name, got)
		}
	}
	for _, name := range []string{"ai_cache_deletions_total", "ai_cache_clears_total"} {
		if got := snapshot.Counters[name+"{cache_type=fixture}"].Value; got != 1 {
			t.Errorf("%s = %v, want 1", name, got)
		}
	}
}

func TestInstrumentedGetWorkflowRecordsTiming(t *testing.T) {
	collector := metrics.NewMemoryCollector()
	engine := NewInstrumentedEngine(orchestration.NewEngine(), collector)
	workflow := orchestration.NewWorkflow("fixture", "workflow", "")
	engine.RegisterWorkflow(workflow)

	if got, err := engine.GetWorkflow(workflow.ID); got != workflow || err != nil {
		t.Errorf("GetWorkflow = %p, %v; want %p", got, err, workflow)
	}
	if got, err := engine.GetWorkflow("missing"); got != nil || err == nil {
		t.Errorf("GetWorkflow for missing ID = %v, %v", got, err)
	}
	timings := collector.Snapshot().Histograms
	for _, id := range []string{workflow.ID, "missing"} {
		key := "ai_workflow_get_duration_seconds{workflow_id=" + id + "}"
		if got := timings[key]; got.Count != 1 || got.Tags["workflow_id"] != id {
			t.Errorf("%s timing = %+v", id, got)
		}
	}
}
