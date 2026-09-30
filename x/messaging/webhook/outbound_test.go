package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spcent/plumego/contract"
)

type staticValueReader struct{}

func (staticValueReader) GetBool(key string, defaultValue bool) bool { return defaultValue }

func (staticValueReader) GetInt(key string, defaultValue int) int { return defaultValue }

func (staticValueReader) GetDurationMs(key string, defaultValueMs int) time.Duration {
	return time.Duration(defaultValueMs) * time.Millisecond
}

func (staticValueReader) GetString(key, defaultValue string) string { return defaultValue }

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("WEBHOOK_ENABLED", "false")
	t.Setenv("WEBHOOK_QUEUE_SIZE", "1")
	t.Setenv("WEBHOOK_WORKERS", "2")
	t.Setenv("WEBHOOK_DRAIN_MAX_MS", "10")
	t.Setenv("WEBHOOK_DROP_POLICY", "drop_newest")
	t.Setenv("WEBHOOK_BLOCK_WAIT_MS", "0")
	t.Setenv("WEBHOOK_DEFAULT_TIMEOUT_MS", "50")
	t.Setenv("WEBHOOK_DEFAULT_MAX_RETRIES", "2")
	t.Setenv("WEBHOOK_BACKOFF_BASE_MS", "5")
	t.Setenv("WEBHOOK_BACKOFF_MAX_MS", "20")
	t.Setenv("WEBHOOK_RETRY_ON_429", "0")
	t.Setenv("WEBHOOK_ALLOW_PRIVATE_NET", "1")

	cfg := ConfigFromEnv()

	if cfg.Enabled != false || cfg.QueueSize != 1 || cfg.Workers != 2 {
		t.Fatalf("unexpected core config: %+v", cfg)
	}
	if cfg.DropPolicy != DropNewest || cfg.BlockWait != 0 {
		t.Fatalf("unexpected drop policy: %+v", cfg)
	}
	if cfg.DefaultTimeout != 50*time.Millisecond || cfg.BackoffMax != 20*time.Millisecond {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
	if cfg.AllowPrivateNetwork != true || cfg.RetryOn429 != false {
		t.Fatalf("unexpected safety config: %+v", cfg)
	}

	// invalid drop policy should fallback
	t.Setenv("WEBHOOK_DROP_POLICY", "invalid")
	cfg = ConfigFromEnv()
	if cfg.DropPolicy != BlockWithLimit {
		t.Fatalf("expected fallback drop policy, got %s", cfg.DropPolicy)
	}

	// min bounds adjustments and bool parsing
	t.Setenv("WEBHOOK_QUEUE_SIZE", "0")
	t.Setenv("WEBHOOK_WORKERS", "0")
	t.Setenv("WEBHOOK_BLOCK_WAIT_MS", "-1")
	t.Setenv("WEBHOOK_RETRY_ON_429", "off")
	cfg = ConfigFromEnv()
	if cfg.QueueSize != 1 || cfg.Workers != 1 || cfg.BlockWait != 0 || cfg.RetryOn429 {
		t.Fatalf("expected bounds adjustments: %+v", cfg)
	}
}

func TestConfigFromReader_RejectsNilReader(t *testing.T) {
	cfg, err := ConfigFromReader(nil)
	if !errors.Is(err, ErrNilValueReader) {
		t.Fatalf("ConfigFromReader error = %v, want ErrNilValueReader", err)
	}
	if cfg != (Config{}) {
		t.Fatalf("ConfigFromReader config = %+v, want zero value", cfg)
	}
}

func TestConfigFromReader_ValidReader(t *testing.T) {
	cfg, err := ConfigFromReader(staticValueReader{})
	if err != nil {
		t.Fatalf("ConfigFromReader error = %v", err)
	}
	if !cfg.Enabled || cfg.QueueSize != 2048 || cfg.Workers != 8 {
		t.Fatalf("unexpected default config: %+v", cfg)
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		allowPN bool
		wantErr bool
	}{
		{"good", "https://1.1.1.1/hook", false, false},
		{"bad scheme", "ftp://example.com", false, true},
		{"private blocked", "http://127.0.0.1:8080", false, true},
		{"private allowed", "http://127.0.0.1", true, false},
		{"dns fail", "http://nonexistent.invalid", false, true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(t.Context(), tt.raw, tt.allowPN)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateTarget(t *testing.T) {
	target := Target{URL: "https://1.1.1.1", Secret: "abcdefgh", Events: []string{"a"}, Name: "ok"}
	if err := validateTarget(target); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bads := []Target{
		{Name: "", Secret: "abcdefgh", URL: "https://a", Events: []string{"a"}},
		{Name: "name", Secret: "short", URL: "https://a", Events: []string{"a"}},
		{Name: "name", Secret: "abcdefgh", URL: "https://a", Events: nil},
	}
	for _, b := range bads {
		if err := validateTarget(b); err == nil {
			t.Fatalf("expected error for %+v", b)
		}
	}
}

func TestQueueEnqueue(t *testing.T) {
	ctx := t.Context()
	q := NewQueue(1, DropNewest, 0)
	if err := q.Enqueue(ctx, Task{DeliveryID: "a"}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if err := q.Enqueue(ctx, Task{DeliveryID: "b"}); err != ErrQueueFull {
		t.Fatalf("expected queue full, got %v", err)
	}

	ctxCancelled, cancel := context.WithCancel(ctx)
	cancel()
	q2 := NewQueue(0, BlockWithLimit, time.Millisecond)
	if err := q2.Enqueue(ctxCancelled, Task{DeliveryID: "c"}); err == nil {
		t.Fatalf("expected context error when cancelled")
	}

	// Block with timeout path
	q3 := NewQueue(1, BlockWithLimit, time.Millisecond)
	_ = q3.Enqueue(ctx, Task{DeliveryID: "first"})
	start := time.Now()
	if err := q3.Enqueue(ctx, Task{DeliveryID: "second"}); err != ErrQueueFull {
		t.Fatalf("expected queue full after wait, got %v", err)
	}
	if time.Since(start) < time.Millisecond {
		t.Fatalf("expected enqueue to wait before failing")
	}

	q3.Close()

	// BlockWithLimit without wait uses immediate fallback
	q4 := NewQueue(1, BlockWithLimit, 0)
	_ = q4.Enqueue(ctx, Task{DeliveryID: "one"})
	if err := q4.Enqueue(ctx, Task{DeliveryID: "two"}); err != ErrQueueFull {
		t.Fatalf("expected immediate full error, got %v", err)
	}

	// FailFast policy path
	q5 := NewQueue(1, FailFast, time.Second)
	_ = q5.Enqueue(ctx, Task{DeliveryID: "one"})
	if err := q5.Enqueue(ctx, Task{DeliveryID: "two"}); err != ErrQueueFull {
		t.Fatalf("expected fail fast full error, got %v", err)
	}
}

func TestDelayScheduler(t *testing.T) {
	q := NewQueue(2, DropNewest, 0)
	ds := NewDelayScheduler(q)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go ds.Run(ctx)
	due := time.Now().Add(10 * time.Millisecond)
	ds.Schedule(due, Task{DeliveryID: "x"})

	select {
	case task := <-q.Chan():
		if task.DeliveryID != "x" {
			t.Fatalf("unexpected task: %+v", task)
		}
	case <-time.After(time.Second):
		t.Fatalf("task not delivered in time")
	}

	ds.Stop()
	cancel()
}

func TestShouldRetryAndBackoff(t *testing.T) {
	if ShouldRetry(200, nil, 1, 3, true) {
		t.Fatalf("success should not retry")
	}
	if !ShouldRetry(500, nil, 1, 3, true) || !ShouldRetry(429, nil, 1, 3, true) {
		t.Fatalf("server errors should retry")
	}
	if ShouldRetry(429, nil, 3, 3, true) {
		t.Fatalf("max attempts reached")
	}

	// Jitter bounds hold for any seed; the global rand source is auto-seeded
	// since Go 1.20, so no deterministic seeding is needed here.

	now := time.Now()
	next := NextBackoff(now, 2, 10*time.Millisecond, 40*time.Millisecond)
	if next.Before(now.Add(5*time.Millisecond)) || next.After(now.Add(40*time.Millisecond)) {
		t.Fatalf("backoff outside expected range: %v", next.Sub(now))
	}

	next = NextBackoff(now, 0, 5*time.Millisecond, 5*time.Millisecond)
	if next.After(now.Add(8 * time.Millisecond)) {
		t.Fatalf("expected attempt normalization to keep within max: %v", next.Sub(now))
	}

	next = NextBackoff(now, 5, time.Second, 2*time.Second)
	if next.After(now.Add(3 * time.Second)) {
		t.Fatalf("backoff should clamp to max")
	}
}

func TestSignAndCompare(t *testing.T) {
	secret := "topsecret"
	ts := "123"
	body := []byte("payload")
	sig := SignV1(secret, ts, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Fatalf("signature mismatch: %s vs %s", sig, expected)
	}
	if !ConstantTimeEqualHex(sig, expected) {
		t.Fatalf("expected constant time equal")
	}
	if ConstantTimeEqualHex("zz", expected) {
		t.Fatalf("invalid hex should not match")
	}
}

func TestMergeAndRewrite(t *testing.T) {
	merged := mergeMeta(map[string]any{"a": 1, "shared": "old"}, map[string]any{"b": 2, "shared": "new"})
	if merged["shared"].(string) != "new" || merged["a"].(int) != 1 || merged["b"].(int) != 2 {
		t.Fatalf("unexpected merge result: %+v", merged)
	}

	raw := []byte(`{"id":"evt","meta":{"delivery_id":"old"}}`)
	updated, err := rewriteDeliveryIDInPayload(raw, "new")
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	if !strings.Contains(string(updated), "new") {
		t.Fatalf("expected new delivery id in payload: %s", updated)
	}

	if _, err := rewriteDeliveryIDInPayload([]byte("{"), "x"); err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestServiceHandleTaskSuccessAndRetry(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Webhook-Id") == "t1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer srv.Close()

	cfg := Config{Enabled: true, QueueSize: 4, Workers: 1, DrainMax: time.Second, DropPolicy: BlockWithLimit, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 2, BackoffBase: time.Millisecond, BackoffMax: 2 * time.Millisecond, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(store, cfg)

	targetSuccess := Target{ID: "t1", Name: "ok", URL: srv.URL, Secret: "supersecr", Events: []string{"evt"}, Enabled: true}
	targetRetry := Target{ID: "t2", Name: "retry", URL: srv.URL, Secret: "supersecr", Events: []string{"evt"}, Enabled: true}
	if _, err := store.CreateTarget(ctx, targetSuccess); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := store.CreateTarget(ctx, targetRetry); err != nil {
		t.Fatalf("create target: %v", err)
	}

	payload := []byte(`{"hello":"world"}`)
	d1 := Delivery{ID: "d1", TargetID: "t1", EventType: "evt", PayloadJSON: payload, Status: DeliveryPending}
	d2 := Delivery{ID: "d2", TargetID: "t2", EventType: "evt", PayloadJSON: payload, Status: DeliveryPending}
	if _, err := store.CreateDelivery(ctx, d1); err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	if _, err := store.CreateDelivery(ctx, d2); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	service.handleTask(ctx, Task{DeliveryID: "d1"})
	service.handleTask(ctx, Task{DeliveryID: "d2"})

	if got, _ := store.GetDelivery(ctx, "d1"); got.Status != DeliverySuccess || got.LastHTTPStatus != 200 || got.LastRespSnippet != "ok" {
		t.Fatalf("unexpected success delivery: %+v", got)
	}
	if got := service.Metrics(); got.SentOK == 0 {
		t.Fatalf("expected success metric increment: %+v", got)
	}

	if got, _ := store.GetDelivery(ctx, "d2"); got.Status != DeliveryRetry || got.NextAt == nil || got.LastHTTPStatus != 500 {
		t.Fatalf("unexpected retry delivery: %+v", got)
	}
	if got := service.Metrics(); got.Retried == 0 {
		t.Fatalf("expected retry metric increment: %+v", got)
	}
}

func TestServiceHandleTaskDeadPayload(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	cfg := Config{Enabled: true, QueueSize: 1, Workers: 1, DrainMax: time.Second, DropPolicy: BlockWithLimit, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(store, cfg)

	target := Target{ID: "tdead", Name: "dead", URL: "http://localhost", Secret: "supersecr", Events: []string{"evt"}, Enabled: true}
	if _, err := store.CreateTarget(ctx, target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	delivery := Delivery{ID: "dead1", TargetID: target.ID, EventType: "evt", Status: DeliveryPending}
	if _, err := store.CreateDelivery(ctx, delivery); err != nil {
		t.Fatalf("create delivery: %v", err)
	}

	service.handleTask(ctx, Task{DeliveryID: delivery.ID})

	got, _ := store.GetDelivery(ctx, delivery.ID)
	if got.Status != DeliveryDead || got.LastError == "" || got.Attempt != 1 {
		t.Fatalf("unexpected dead delivery: %+v", got)
	}
	if service.Metrics().Dead == 0 {
		t.Fatalf("expected dead metric increment")
	}
}

func TestServiceLifecycleAndTargets(t *testing.T) {
	ctx := t.Context()
	cfg := Config{Enabled: true, QueueSize: 2, Workers: 1, DrainMax: time.Millisecond * 50, DropPolicy: DropNewest, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond * 10, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(NewMemStore(), cfg)

	service.Start(ctx)
	if !service.running.Load() {
		t.Fatalf("service should be running")
	}
	// second start should be no-op
	service.Start(ctx)
	service.Stop()
	// second stop should be no-op
	service.Stop()

	cfg.Enabled = false
	serviceDisabled := NewService(NewMemStore(), cfg)
	serviceDisabled.Start(ctx)
}

func TestServiceCreateAndUpdateTarget(t *testing.T) {
	ctx := t.Context()
	cfg := Config{Enabled: true, QueueSize: 2, Workers: 1, DrainMax: time.Second, DropPolicy: DropNewest, BlockWait: time.Millisecond, DefaultTimeout: 150 * time.Millisecond, DefaultMaxRetries: 3, BackoffBase: time.Millisecond * 2, BackoffMax: time.Millisecond * 10, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(NewMemStore(), cfg)

	created, err := service.CreateTarget(ctx, Target{Name: "name", URL: "http://8.8.8.8", Secret: "abcdefgh", Events: []string{"evt"}, Enabled: true})
	if err != nil {
		t.Fatalf("create target error: %v", err)
	}
	if created.ID == "" || created.TimeoutMs == 0 || created.MaxRetries != cfg.DefaultMaxRetries {
		t.Fatalf("defaults not applied: %+v", created)
	}

	newName := "updated"
	newURL := "http://1.1.1.1"
	newSecret := "abcdefghijk"
	newEvents := []string{"evt", "other"}
	newHeaders := map[string]string{"K": "V"}
	tm := 200
	mr := 4
	bb := 5
	bm := 25
	retry429 := false
	patch := TargetPatch{&newName, &newURL, &newSecret, &newEvents, boolPtr(false), &newHeaders, &tm, &mr, &bb, &bm, &retry429}
	updated, err := service.UpdateTarget(ctx, created.ID, patch)
	if err != nil {
		t.Fatalf("update target error: %v", err)
	}
	if updated.Name != newName || updated.URL != newURL || updated.Secret != newSecret || len(updated.Events) != 2 || updated.Enabled {
		t.Fatalf("patch not applied: %+v", updated)
	}

	if t2, ok := service.GetTarget(ctx, created.ID); !ok || t2.ID != created.ID {
		t.Fatalf("get target failed: %+v", t2)
	}
	list, _ := service.ListTargets(ctx, TargetFilter{Enabled: boolPtr(false), Event: "OTHER"})
	if len(list) != 1 {
		t.Fatalf("expected filtered target")
	}

	if _, err := service.UpdateTarget(ctx, created.ID, TargetPatch{Events: &[]string{}}); err == nil {
		t.Fatalf("expected validation error for empty events")
	}
	if _, err := service.UpdateTarget(ctx, created.ID, TargetPatch{Secret: func() *string { s := "short"; return &s }()}); err == nil {
		t.Fatalf("expected validation error for secret")
	}

	strictCfg := cfg
	strictCfg.AllowPrivateNetwork = false
	serviceStrict := NewService(NewMemStore(), strictCfg)
	strictTarget, _ := serviceStrict.CreateTarget(ctx, Target{Name: "strict", URL: "http://8.8.8.8", Secret: "abcdefgh", Events: []string{"evt"}, Enabled: true})
	badURL := "http://127.0.0.1"
	if _, err := serviceStrict.UpdateTarget(ctx, strictTarget.ID, TargetPatch{URL: &badURL}); err == nil {
		t.Fatalf("expected private network validation error")
	}
}

func TestServiceWorkerLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cfg := Config{Enabled: true, QueueSize: 2, Workers: 1, DrainMax: time.Second, DropPolicy: DropNewest, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond * 2, RetryOn429: true, AllowPrivateNetwork: true}
	store := NewMemStore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()

	target := Target{ID: "tw", Name: "worker", URL: srv.URL, Secret: "abcdefgh", Events: []string{"evt"}, Enabled: true}
	_, _ = store.CreateTarget(ctx, target)
	delivery := Delivery{ID: "dw", TargetID: target.ID, EventType: "evt", PayloadJSON: []byte(`{"x":1}`), Status: DeliveryPending}
	_, _ = store.CreateDelivery(ctx, delivery)

	service := NewService(store, cfg)
	service.Start(ctx)
	defer service.Stop()

	if err := service.enqueue(ctx, Task{DeliveryID: delivery.ID}); err != nil {
		t.Fatalf("enqueue error: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if d, _ := store.GetDelivery(ctx, delivery.ID); d.Status == DeliverySuccess {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery not processed by worker loop")
}

func TestEnqueueDisabled(t *testing.T) {
	cfg := Config{Enabled: false, QueueSize: 1, Workers: 1, DrainMax: time.Second, DropPolicy: DropNewest, BlockWait: 0, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond, RetryOn429: true}
	service := NewService(NewMemStore(), cfg)
	err := service.enqueue(t.Context(), Task{DeliveryID: "x"})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestTriggerEventAndReplay(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	cfg := Config{Enabled: true, QueueSize: 4, Workers: 1, DrainMax: time.Second, DropPolicy: BlockWithLimit, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(store, cfg)

	target := Target{ID: "t1", Name: "ok", URL: "http://localhost", Secret: "supersecr", Events: []string{"evt"}, Enabled: true}
	if _, err := store.CreateTarget(ctx, target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	n, err := service.TriggerEvent(ctx, Event{Type: "evt"})
	if err != nil || n != 1 {
		t.Fatalf("unexpected trigger result: %d %v", n, err)
	}

	// consume enqueued task
	task := <-service.queue.Chan()
	if task.DeliveryID == "" {
		t.Fatalf("expected delivery id")
	}

	// replay delivery
	deliveries, _ := store.ListDeliveries(ctx, DeliveryFilter{})
	original := deliveries[0]

	if d, ok := service.GetDelivery(ctx, original.ID); !ok || d.ID == "" {
		t.Fatalf("get delivery failed: %+v", d)
	}
	listed, _ := service.ListDeliveries(ctx, DeliveryFilter{Limit: 10})
	if len(listed) == 0 {
		t.Fatalf("expected list deliveries to return results")
	}
	replayed, err := service.ReplayDelivery(ctx, original.ID)
	if err != nil {
		t.Fatalf("replay error: %v", err)
	}
	if replayed.ID == original.ID || len(replayed.PayloadJSON) == 0 {
		t.Fatalf("replay did not create new delivery: %+v", replayed)
	}
	if !strings.Contains(string(replayed.PayloadJSON), "delivery_id") {
		t.Fatalf("replayed payload missing id: %s", replayed.PayloadJSON)
	}
}

func TestOutboundListTargetsInvalidEnabledQuery(t *testing.T) {
	svc := NewService(NewMemStore(), Config{})

	req := httptest.NewRequest(http.MethodGet, "/webhooks/targets?enabled=maybe", nil)
	rec := httptest.NewRecorder()
	webhookListTargets(rec, req, svc)

	assertWebhookErrorCode(t, rec, http.StatusBadRequest, contract.CodeBadRequest, "invalid enabled query")
}

func TestOutboundHandlersRequireRouteParams(t *testing.T) {
	svc := NewService(NewMemStore(), Config{})

	tests := []struct {
		name    string
		field   string
		handler func(http.ResponseWriter, *http.Request)
		request *http.Request
	}{
		{
			name:    "get target",
			field:   "id",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookGetTarget(w, r, svc) },
			request: httptest.NewRequest(http.MethodGet, "/webhooks/targets/", nil),
		},
		{
			name:    "patch target",
			field:   "id",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookPatchTarget(w, r, svc) },
			request: httptest.NewRequest(http.MethodPatch, "/webhooks/targets/", strings.NewReader(`{}`)),
		},
		{
			name:    "set target enabled",
			field:   "id",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookSetTargetEnabled(w, r, svc, true) },
			request: httptest.NewRequest(http.MethodPost, "/webhooks/targets//enable", nil),
		},
		{
			name:    "trigger event",
			field:   "event",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookTriggerEvent(w, r, svc, "", true) },
			request: httptest.NewRequest(http.MethodPost, "/webhooks/events/", strings.NewReader(`{}`)),
		},
		{
			name:    "get delivery",
			field:   "id",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookGetDelivery(w, r, svc) },
			request: httptest.NewRequest(http.MethodGet, "/webhooks/deliveries/", nil),
		},
		{
			name:    "replay delivery",
			field:   "id",
			handler: func(w http.ResponseWriter, r *http.Request) { webhookReplayDelivery(w, r, svc) },
			request: httptest.NewRequest(http.MethodPost, "/webhooks/deliveries//replay", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler(rec, tt.request)

			assertWebhookErrorCode(t, rec, http.StatusBadRequest, contract.CodeRequired, tt.field+" is required")
		})
	}
}

func TestOutboundHandlersUseTypedResponseShapes(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	cfg := Config{
		Enabled:             true,
		QueueSize:           4,
		Workers:             1,
		DrainMax:            time.Second,
		DropPolicy:          BlockWithLimit,
		BlockWait:           time.Millisecond,
		DefaultTimeout:      time.Second,
		DefaultMaxRetries:   1,
		BackoffBase:         time.Millisecond,
		BackoffMax:          time.Millisecond,
		RetryOn429:          true,
		AllowPrivateNetwork: true,
	}
	svc := NewService(store, cfg)

	target, err := svc.CreateTarget(ctx, Target{
		ID:      "target-1",
		Name:    "typed",
		URL:     "http://8.8.8.8",
		Secret:  "abcdefgh",
		Events:  []string{"demo"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}

	delivery := Delivery{
		ID:          "delivery-1",
		TargetID:    target.ID,
		EventID:     "event-1",
		EventType:   "demo",
		PayloadJSON: []byte(`{"x":1}`),
		Status:      DeliveryPending,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if _, err := store.CreateDelivery(ctx, delivery); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}

	listTargetsReq := httptest.NewRequest(http.MethodGet, "/webhooks/targets?enabled=true", nil)
	listTargetsRec := httptest.NewRecorder()
	webhookListTargets(listTargetsRec, listTargetsReq, svc)
	if listTargetsRec.Code != http.StatusOK {
		t.Fatalf("list targets status = %d, want %d", listTargetsRec.Code, http.StatusOK)
	}
	targets := decodeWebhookData[targetListResponse](t, listTargetsRec)
	if len(targets.Items) != 1 || targets.Items[0].ID != target.ID {
		t.Fatalf("unexpected targets response: %+v", targets)
	}

	triggerReq := requestWithWebhookParam(http.MethodPost, "/webhooks/events/demo", strings.NewReader(`{"data":{"ok":true},"meta":{}}`), "event", "demo")
	triggerRec := httptest.NewRecorder()
	webhookTriggerEvent(triggerRec, triggerReq, svc, "", true)
	if triggerRec.Code != http.StatusAccepted {
		t.Fatalf("trigger status = %d, want %d", triggerRec.Code, http.StatusAccepted)
	}
	trigger := decodeWebhookData[triggerEventResponse](t, triggerRec)
	if trigger.Event != "demo" || trigger.Enqueued != 1 {
		t.Fatalf("unexpected trigger response: %+v", trigger)
	}

	listDeliveriesReq := httptest.NewRequest(http.MethodGet, "/webhooks/deliveries", nil)
	listDeliveriesRec := httptest.NewRecorder()
	webhookListDeliveries(listDeliveriesRec, listDeliveriesReq, svc, 20)
	if listDeliveriesRec.Code != http.StatusOK {
		t.Fatalf("list deliveries status = %d, want %d", listDeliveriesRec.Code, http.StatusOK)
	}
	deliveries := decodeWebhookData[deliveryListResponse](t, listDeliveriesRec)
	if len(deliveries.Items) == 0 {
		t.Fatal("expected deliveries response items")
	}

	getDeliveryReq := requestWithWebhookParam(http.MethodGet, "/webhooks/deliveries/delivery-1", strings.NewReader(""), "id", "delivery-1")
	getDeliveryRec := httptest.NewRecorder()
	webhookGetDelivery(getDeliveryRec, getDeliveryReq, svc)
	if getDeliveryRec.Code != http.StatusOK {
		t.Fatalf("get delivery status = %d, want %d", getDeliveryRec.Code, http.StatusOK)
	}
	detail := decodeWebhookData[deliveryDetailResponse](t, getDeliveryRec)
	if detail.ID != delivery.ID || len(detail.PayloadJSON) == 0 {
		t.Fatalf("unexpected delivery detail response: %+v", detail)
	}

	replayReq := requestWithWebhookParam(http.MethodPost, "/webhooks/deliveries/delivery-1/replay", strings.NewReader(""), "id", "delivery-1")
	replayRec := httptest.NewRecorder()
	webhookReplayDelivery(replayRec, replayReq, svc)
	if replayRec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want %d", replayRec.Code, http.StatusOK)
	}
	replay := decodeWebhookData[replayDeliveryResponse](t, replayRec)
	if !replay.OK || replay.DeliveryID == "" {
		t.Fatalf("unexpected replay response: %+v", replay)
	}
}

func TestTriggerEventDropped(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	cfg := Config{Enabled: true, QueueSize: 0, Workers: 0, DrainMax: time.Second, DropPolicy: DropNewest, BlockWait: 0, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond, RetryOn429: true, AllowPrivateNetwork: true}
	service := NewService(store, cfg)
	target := Target{ID: "drop", Name: "drop", URL: "http://8.8.8.8", Secret: "abcdefgh", Events: []string{"evt"}, Enabled: true}
	_, _ = store.CreateTarget(ctx, target)

	count, err := service.TriggerEvent(ctx, Event{Type: "evt"})
	if err != nil || count != 0 {
		t.Fatalf("expected zero enqueues when queue full: %d %v", count, err)
	}
	if service.Metrics().Dropped == 0 {
		t.Fatalf("expected dropped metric increment")
	}
}

func TestReplayDeliveryErrors(t *testing.T) {
	ctx := t.Context()
	service := NewService(NewMemStore(), Config{})

	if _, err := service.ReplayDelivery(ctx, "missing"); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected not found error")
	}

	d := Delivery{ID: "no_payload", TargetID: "t", EventType: "evt"}
	_, _ = service.store.CreateDelivery(ctx, d)
	if _, err := service.ReplayDelivery(ctx, d.ID); err == nil {
		t.Fatalf("expected payload error on replay")
	}
}

func TestHandleTaskFailureNoRetry(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	cfg := Config{Enabled: true, QueueSize: 1, Workers: 1, DrainMax: time.Second, DropPolicy: DropNewest, BlockWait: time.Millisecond, DefaultTimeout: time.Second, DefaultMaxRetries: 1, BackoffBase: time.Millisecond, BackoffMax: time.Millisecond, RetryOn429: true, AllowPrivateNetwork: true}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()

	target := Target{ID: "nf", Name: "nf", URL: srv.URL, Secret: "abcdefgh", Events: []string{"evt"}, Enabled: true, RetryOn429: boolPtr(false), MaxRetries: 1}
	_, _ = store.CreateTarget(ctx, target)
	delivery := Delivery{ID: "nf1", TargetID: target.ID, EventType: "evt", PayloadJSON: []byte(`{"y":1}`), Status: DeliveryPending}
	_, _ = store.CreateDelivery(ctx, delivery)

	service := NewService(store, cfg)
	service.handleTask(ctx, Task{DeliveryID: delivery.ID})

	if got, _ := store.GetDelivery(ctx, delivery.ID); got.Status != DeliveryDead || got.LastHTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("expected non-retry to mark dead: %+v", got)
	}
}

func TestTriggerEventValidationError(t *testing.T) {
	service := NewService(NewMemStore(), Config{})
	if _, err := service.TriggerEvent(t.Context(), Event{}); err == nil {
		t.Fatalf("expected event type error")
	}
}

func TestSendOnceValidationError(t *testing.T) {
	ctx := t.Context()
	cfg := Config{AllowPrivateNetwork: false, DefaultTimeout: time.Second}
	service := NewService(NewMemStore(), cfg)
	target := Target{ID: "t", URL: "http://127.0.0.1", Secret: "abcdefgh", Headers: map[string]string{"X": "1"}}
	delivery := Delivery{ID: "d", EventType: "evt"}
	status, snippet, err := service.sendOnce(ctx, target, delivery, []byte("{}"), 1)
	if err == nil || status != 0 || snippet != "" {
		t.Fatalf("expected validation error without network call")
	}
}

func TestStoreAndFilters(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()

	t1 := Target{ID: "a", Name: "a", URL: "http://8.8.8.8", Secret: "abcdefgh", Events: []string{"*"}, Enabled: true}
	t2 := Target{ID: "b", Name: "b", URL: "http://8.8.4.4", Secret: "abcdefgh", Events: []string{"specific"}, Enabled: false}
	_, _ = store.CreateTarget(ctx, t1)
	_, _ = store.CreateTarget(ctx, t2)
	list, _ := store.ListTargets(ctx, TargetFilter{Enabled: boolPtr(true), Event: "SPECIFIC"})
	if len(list) != 1 || list[0].ID != "a" {
		t.Fatalf("event filter failed: %+v", list)
	}

	now := time.Now().UTC()
	d1 := Delivery{ID: "1", TargetID: "a", EventType: "specific", PayloadJSON: []byte("{}"), Status: DeliveryPending}
	d2 := Delivery{ID: "2", TargetID: "b", EventType: "specific", PayloadJSON: []byte("{}"), Status: DeliveryFailed}
	d3 := Delivery{ID: "3", TargetID: "a", EventType: "other", PayloadJSON: []byte("{}"), Status: DeliveryPending}
	_, _ = store.CreateDelivery(ctx, d1)
	_, _ = store.CreateDelivery(ctx, d2)
	_, _ = store.CreateDelivery(ctx, d3)

	d := store.deliveries["1"]
	d.CreatedAt = now.Add(-2 * time.Hour)
	store.deliveries["1"] = d
	d = store.deliveries["2"]
	d.CreatedAt = now.Add(-time.Hour)
	store.deliveries["2"] = d
	d = store.deliveries["3"]
	d.CreatedAt = now
	store.deliveries["3"] = d

	from := now.Add(-3 * time.Hour)
	to := now.Add(30 * time.Minute)
	filter := DeliveryFilter{TargetID: &t1.ID, Status: &d1.Status, From: &from, To: &to, Limit: 2, Cursor: "1"}
	deliveries, _ := store.ListDeliveries(ctx, filter)
	if len(deliveries) != 1 || deliveries[0].ID != "3" {
		t.Fatalf("delivery filter unexpected: %+v", deliveries)
	}

	if _, err := store.UpdateTarget(ctx, "missing", TargetPatch{}); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("expected not found on update target")
	}

	if eventMatch([]string{"a"}, "b") {
		t.Fatalf("unexpected match for different event")
	}
}

func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"172.20.1.1", true},
		{"192.168.1.5", true},
		{"169.254.1.1", true},
		{"8.8.8.8", false},
		{"127.0.0.1", true},
		{"::1", true},
		{"fc00::1", true},
		{"2001:4860:4860::8888", false},
	}
	for _, tt := range cases {
		if got := isPrivateIP(net.ParseIP(tt.ip)); got != tt.want {
			t.Fatalf("ip %s expected %v got %v", tt.ip, tt.want, got)
		}
	}
}

func TestEnvHelpers(t *testing.T) {
	reader := envReader{}

	if reader.GetString("MISSING", "default") != "default" {
		t.Fatalf("GetString default failed")
	}

	t.Setenv("TEST_INT", "notanint")
	if reader.GetInt("TEST_INT", 5) != 5 {
		t.Fatalf("GetInt fallback failed")
	}

	t.Setenv("TEST_BOOL", "yes")
	if !reader.GetBool("TEST_BOOL", false) {
		t.Fatalf("GetBool parsing failed")
	}

	if reader.GetDurationMs("TEST_DURATION", 10) != 10*time.Millisecond {
		t.Fatalf("GetDuration default failed")
	}

	t.Setenv("TEST_BOOL_FALSE", "off")
	if reader.GetBool("TEST_BOOL_FALSE", true) {
		t.Fatalf("GetBool should parse false")
	}

	t.Setenv("TEST_BOOL_INVALID", "maybe")
	if reader.GetBool("TEST_BOOL_INVALID", true) != true {
		t.Fatalf("GetBool should fallback to default")
	}
}

func TestMSHelper(t *testing.T) {
	if ms(0, 5*time.Millisecond) != 5*time.Millisecond {
		t.Fatalf("expected default duration")
	}
	if ms(10, 0) != 10*time.Millisecond {
		t.Fatalf("expected milliseconds conversion")
	}
}

func decodeWebhookData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}
