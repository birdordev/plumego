package abuse

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				Rate:            10.0,
				Capacity:        100,
				MaxEntries:      1000,
				CleanupInterval: time.Minute,
				MaxIdle:         5 * time.Minute,
				Shards:          4,
			},
			wantErr: false,
		},
		{
			name: "negative rate",
			config: Config{
				Rate:     -1.0,
				Capacity: 100,
			},
			wantErr: true,
		},
		{
			name: "zero capacity uses default",
			config: Config{
				Rate:     10.0,
				Capacity: 0,
			},
			wantErr: false,
		},
		{
			name: "negative max entries",
			config: Config{
				Rate:       10.0,
				Capacity:   100,
				MaxEntries: -10,
			},
			wantErr: true,
		},
		{
			name: "zero cleanup interval uses default",
			config: Config{
				Rate:            10.0,
				Capacity:        100,
				CleanupInterval: 0,
			},
			wantErr: false,
		},
		{
			name: "negative max idle",
			config: Config{
				Rate:     10.0,
				Capacity: 100,
				MaxIdle:  -1,
			},
			wantErr: true,
		},
		{
			name: "zero shards uses default",
			config: Config{
				Rate:     10.0,
				Capacity: 100,
				Shards:   0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidateMatchesStrictConstructor(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{name: "zero values omitted", config: Config{}, wantErr: false},
		{name: "partial zero values omitted", config: Config{Rate: 10, Capacity: 0, Shards: 0}, wantErr: false},
		{name: "negative value rejected", config: Config{MaxIdle: -1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validateErr := tt.config.Validate()
			limiter, constructorErr := NewLimiterWithConfig(tt.config)
			if limiter != nil {
				defer limiter.Stop()
			}

			if (validateErr != nil) != tt.wantErr {
				t.Fatalf("Validate error = %v, wantErr %v", validateErr, tt.wantErr)
			}
			if (constructorErr != nil) != tt.wantErr {
				t.Fatalf("NewLimiterWithConfig error = %v, wantErr %v", constructorErr, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(validateErr, ErrInvalidConfig) {
				t.Fatalf("Validate error = %v, want ErrInvalidConfig", validateErr)
			}
			if tt.wantErr && !errors.Is(constructorErr, ErrInvalidConfig) {
				t.Fatalf("NewLimiterWithConfig error = %v, want ErrInvalidConfig", constructorErr)
			}
		})
	}
}

func TestNewLimiterWithConfigDefaultsAndInvalidConfig(t *testing.T) {
	limiter, err := NewLimiterWithConfig(Config{})
	if err != nil {
		t.Fatalf("NewLimiterWithConfig default config: %v", err)
	}
	defer limiter.Stop()

	if decision := limiter.Allow("default-key"); !decision.Allowed {
		t.Fatalf("default limiter should allow first request")
	}

	tests := []struct {
		name   string
		config Config
	}{
		{name: "negative rate", config: Config{Rate: -1}},
		{name: "negative capacity", config: Config{Capacity: -1}},
		{name: "negative max entries", config: Config{MaxEntries: -1}},
		{name: "negative cleanup interval", config: Config{CleanupInterval: -1}},
		{name: "negative max idle", config: Config{MaxIdle: -1}},
		{name: "negative shards", config: Config{Shards: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewLimiterWithConfig(tt.config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewLimiterWithConfig error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewLimiterFailsClosedForInvalidConfig(t *testing.T) {
	limiter := NewLimiter(Config{Rate: -1})
	if limiter.Allow("key").Allowed {
		t.Fatalf("NewLimiter with invalid config should fail closed (deny all)")
	}
	limiter.Stop()
}

func TestZeroValueLimiterFailsClosed(t *testing.T) {
	var limiter Limiter
	decision := limiter.Allow("key")
	if decision.Allowed {
		t.Fatalf("zero-value limiter should fail closed")
	}
	if limiter.Metrics().Rejected != 1 {
		t.Fatalf("zero-value limiter rejected metrics = %d, want 1", limiter.Metrics().Rejected)
	}
	limiter.Stop()

	var nilLimiter *Limiter
	if nilLimiter.Allow("key").Allowed {
		t.Fatalf("nil limiter should fail closed")
	}
	nilLimiter.Stop()
}

func TestLimiterMetricsReturnsSnapshot(t *testing.T) {
	limiter := NewLimiter(Config{
		Rate:            1,
		Capacity:        1,
		MaxEntries:      10,
		CleanupInterval: time.Minute,
		MaxIdle:         time.Minute,
		Shards:          1,
	})
	defer limiter.Stop()

	limiter.Allow("snapshot")
	snapshot := limiter.Metrics()
	snapshot.Allowed = 0

	if got := limiter.Metrics().Allowed; got == 0 {
		t.Fatalf("mutating returned metrics snapshot changed internal counters")
	}
}

func TestLimiterAllowKeyRejectsEmptyKey(t *testing.T) {
	limiter := NewLimiter(DefaultConfig())
	defer limiter.Stop()

	decision, err := limiter.AllowKey("")
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("AllowKey empty error = %v, want ErrInvalidKey", err)
	}
	if decision.Allowed {
		t.Fatal("AllowKey empty decision should be denied")
	}
	if got := limiter.Metrics().Rejected; got != 1 {
		t.Fatalf("AllowKey empty rejected metrics = %d, want 1", got)
	}

	decision, err = limiter.AllowKey("subject-1")
	if err != nil {
		t.Fatalf("AllowKey valid error = %v", err)
	}
	if !decision.Allowed {
		t.Fatal("AllowKey valid key should be allowed")
	}
}

func TestLimiterObjectPool(t *testing.T) {
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      100,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          2,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Create and delete many buckets to test object pool
	for i := 0; i < 50; i++ {
		key := "pool-test-" + string(rune(i))
		decision := limiter.Allow(key)
		if !decision.Allowed {
			t.Errorf("Expected request to be allowed, got denied")
		}
	}

	// Wait for cleanup to potentially reuse buckets
	time.Sleep(time.Millisecond * 150)
}

func TestLimiterShardCounter(t *testing.T) {
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Create some entries
	for i := 0; i < 10; i++ {
		key := "counter-test-" + string(rune(i))
		limiter.Allow(key)
	}

	// Test that the limiter is working correctly
	decision := limiter.Allow("test-key")
	if decision.Limit != 100 {
		t.Errorf("Expected limit to be 100, got %d", decision.Limit)
	}
}

func TestLimiterConcurrentPerformance(t *testing.T) {
	config := Config{
		Rate:            100.0,
		Capacity:        1000,
		MaxEntries:      10000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          16,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	var wg sync.WaitGroup
	var totalAllowed, totalRejected int64

	// Start multiple goroutines to test concurrent performance
	numGoroutines := 20
	operationsPerGoroutine := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < operationsPerGoroutine; j++ {
				key := "concurrent-" + string(rune(id)) + "-" + string(rune(j))
				decision := limiter.Allow(key)

				if decision.Allowed {
					atomic.AddInt64(&totalAllowed, 1)
				} else {
					atomic.AddInt64(&totalRejected, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	totalOperations := int64(numGoroutines * operationsPerGoroutine)
	if totalOperations != totalAllowed+totalRejected {
		t.Errorf("Total operations mismatch: expected %d, got %d", totalOperations, totalAllowed+totalRejected)
	}

	// Should have some allowed requests
	if totalAllowed == 0 {
		t.Errorf("Expected some requests to be allowed, got 0")
	}
}

func TestLimiterTOCTOUFix(t *testing.T) {
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      10, // Small limit to trigger eviction
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Pre-populate with entries
	for i := 0; i < 5; i++ {
		key := "key-" + string(rune(i))
		limiter.Allow(key)
		time.Sleep(time.Millisecond * 10)
	}

	// Start concurrent operations that may trigger TOCTOU
	var wg sync.WaitGroup
	var errors int64

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				key := "concurrent-key-" + string(rune(id)) + "-" + string(rune(j))
				decision := limiter.Allow(key)
				if !decision.Allowed {
					atomic.AddInt64(&errors, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	// Should not have excessive errors due to TOCTOU race
	if errors > 200 { // Allow some errors due to rate limiting
		t.Errorf("Too many errors, possible TOCTOU issue: %d", errors)
	}
}

func TestLimiterConfigurationDefaults(t *testing.T) {
	config := DefaultConfig()

	if config.Rate <= 0 {
		t.Errorf("Default rate should be positive, got %f", config.Rate)
	}
	if config.Capacity <= 0 {
		t.Errorf("Default capacity should be positive, got %d", config.Capacity)
	}
	if config.MaxEntries <= 0 {
		t.Errorf("Default max entries should be positive, got %d", config.MaxEntries)
	}
	if config.CleanupInterval <= 0 {
		t.Errorf("Default cleanup interval should be positive, got %v", config.CleanupInterval)
	}
	if config.MaxIdle <= 0 {
		t.Errorf("Default max idle should be positive, got %v", config.MaxIdle)
	}
	if config.Shards <= 0 {
		t.Errorf("Default shards should be positive, got %d", config.Shards)
	}
}

func TestLimiterWithCustomNowFunction(t *testing.T) {
	var currentTime time.Time
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
		Now:             func() time.Time { return currentTime },
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	currentTime = time.Now()
	decision := limiter.Allow("test-key")
	if !decision.Allowed {
		t.Errorf("Expected request to be allowed with custom Now function")
	}
}

func TestLimiterStop(t *testing.T) {
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
	}

	limiter := NewLimiter(config)

	// Test that Stop can be called multiple times
	limiter.Stop()
	limiter.Stop()
	limiter.Stop()

	// Should not panic
}

func BenchmarkLimiterAllow(b *testing.B) {
	config := Config{
		Rate:            1000.0,
		Capacity:        10000,
		MaxEntries:      100000,
		CleanupInterval: time.Minute,
		MaxIdle:         5 * time.Minute,
		Shards:          16,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "bench-key-" + string(rune(i%1000))
			limiter.Allow(key)
			i++
		}
	})
}

func BenchmarkLimiterStressTest(b *testing.B) {
	config := Config{
		Rate:            100.0,
		Capacity:        1000,
		MaxEntries:      10000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          16,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			key := fmt.Sprintf("stress-key-%d", rand.Int63())
			limiter.Allow(key)
		}
	})
}

func TestLimiterExtremeConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "very high rate",
			config: Config{
				Rate:            1000000.0,
				Capacity:        1000000,
				MaxEntries:      1000000,
				CleanupInterval: time.Millisecond * 10,
				MaxIdle:         time.Millisecond * 100,
				Shards:          64,
			},
		},
		{
			name: "very low rate",
			config: Config{
				Rate:            0.001,
				Capacity:        1,
				MaxEntries:      1000,
				CleanupInterval: time.Minute,
				MaxIdle:         time.Hour,
				Shards:          1,
			},
		},
		{
			name: "maximum capacity",
			config: Config{
				Rate:            1000.0,
				Capacity:        1000000,
				MaxEntries:      1000000,
				CleanupInterval: time.Minute,
				MaxIdle:         time.Hour,
				Shards:          16,
			},
		},
		{
			name: "minimum capacity",
			config: Config{
				Rate:            10.0,
				Capacity:        1,
				MaxEntries:      100,
				CleanupInterval: time.Millisecond * 100,
				MaxIdle:         time.Second * 5,
				Shards:          1,
			},
		},
		{
			name: "many shards",
			config: Config{
				Rate:            100.0,
				Capacity:        1000,
				MaxEntries:      10000,
				CleanupInterval: time.Minute,
				MaxIdle:         time.Minute * 10,
				Shards:          256,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewLimiter(tt.config)
			defer limiter.Stop()

			// Test basic functionality
			decision := limiter.Allow("test-key")
			if !decision.Allowed {
				t.Errorf("Expected request to be allowed with extreme config")
			}

			// Test multiple keys
			for i := 0; i < 10; i++ {
				key := fmt.Sprintf("key-%d", i)
				limiter.Allow(key)
			}

			// Test metrics
			metrics := limiter.Metrics()
			if metrics.Allowed == 0 {
				t.Errorf("Expected some allowed requests")
			}
		})
	}
}

func TestLimiterBoundaryConditions(t *testing.T) {
	config := Config{
		Rate:            1.0,
		Capacity:        1,
		MaxEntries:      10,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          1,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Test exact capacity
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("boundary-key-%d", i)
		decision := limiter.Allow(key)
		if !decision.Allowed {
			t.Errorf("Expected request %d to be allowed", i)
		}
	}

	// Test eviction boundary
	decision := limiter.Allow("boundary-test-key")
	if !decision.Allowed {
		t.Errorf("Expected boundary test key to be allowed")
	}

	// Test rate limiting boundary
	time.Sleep(time.Second)
	decision = limiter.Allow("rate-test-key")
	if !decision.Allowed {
		t.Errorf("Expected rate test key to be allowed after sleep")
	}
}

func TestLimiterMetricsAccuracy(t *testing.T) {
	config := Config{
		Rate:            100.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Allow some requests
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("metrics-key-%d", i)
		limiter.Allow(key)
	}

	// Check metrics
	metrics := limiter.Metrics()
	allowed := metrics.Allowed
	rejected := metrics.Rejected
	total := allowed + rejected

	if total != 50 {
		t.Errorf("Expected total metrics to be 50, got %d", total)
	}

	if allowed == 0 {
		t.Errorf("Expected some allowed requests")
	}
	if buckets := metrics.Buckets; buckets != 50 {
		t.Errorf("Expected bucket metrics to be 50, got %d", buckets)
	}
}

func TestLimiterBucketMetricsAfterEvictionAndCleanup(t *testing.T) {
	currentTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := Config{
		Rate:            100.0,
		Capacity:        100,
		MaxEntries:      2,
		CleanupInterval: time.Hour,
		MaxIdle:         time.Minute,
		Shards:          4,
		Now:             func() time.Time { return currentTime },
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	limiter.Allow("bucket-a")
	limiter.Allow("bucket-b")
	if got := limiter.Metrics().Buckets; got != 2 {
		t.Fatalf("Buckets after create = %d, want 2", got)
	}

	currentTime = currentTime.Add(time.Second)
	limiter.Allow("bucket-c")
	if got := limiter.Metrics().Buckets; got != 2 {
		t.Fatalf("Buckets after eviction = %d, want 2", got)
	}
	if got := limiter.Metrics().Evictions; got == 0 {
		t.Fatalf("expected eviction metric to increment")
	}

	currentTime = currentTime.Add(2 * time.Minute)
	limiter.cleanup(currentTime)
	if got := limiter.Metrics().Buckets; got != 0 {
		t.Fatalf("Buckets after cleanup = %d, want 0", got)
	}
}

func TestLimiterConcurrentMetrics(t *testing.T) {
	config := Config{
		Rate:            1000.0,
		Capacity:        1000,
		MaxEntries:      10000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          16,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	var wg sync.WaitGroup
	numGoroutines := 20
	operationsPerGoroutine := 100

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				key := fmt.Sprintf("concurrent-metrics-%d-%d", id, j)
				limiter.Allow(key)
			}
		}(i)
	}

	wg.Wait()

	// Check metrics consistency
	metrics := limiter.Metrics()
	allowed := metrics.Allowed
	rejected := metrics.Rejected
	total := allowed + rejected
	expectedTotal := int64(numGoroutines * operationsPerGoroutine)

	if total != expectedTotal {
		t.Errorf("Expected total metrics %d, got %d", expectedTotal, total)
	}
}

func TestLimiterCleanupUnderLoad(t *testing.T) {
	config := Config{
		Rate:            100.0,
		Capacity:        100,
		MaxEntries:      100,
		CleanupInterval: time.Millisecond * 50,
		MaxIdle:         time.Millisecond * 100,
		Shards:          4,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Pre-populate with entries
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("cleanup-key-%d", i)
		limiter.Allow(key)
		time.Sleep(time.Millisecond * 10)
	}

	// Start concurrent operations while cleanup is happening
	var wg sync.WaitGroup
	wg.Add(10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				key := fmt.Sprintf("cleanup-concurrent-%d-%d", id, j)
				limiter.Allow(key)
				time.Sleep(time.Millisecond * 5)
			}
		}(i)
	}

	wg.Wait()

	// Wait for cleanup to run
	time.Sleep(time.Millisecond * 150)

	// Should not panic or deadlock
	metrics := limiter.Metrics()
	if metrics.Allowed == 0 {
		t.Errorf("Expected some allowed requests during cleanup")
	}
}

func TestLimiterMemoryUsage(t *testing.T) {
	config := Config{
		Rate:            100.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          16,
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Create many entries
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("memory-test-key-%d", i)
		limiter.Allow(key)
	}

	// Force garbage collection
	runtime.GC()

	// Check that we can still operate
	decision := limiter.Allow("memory-test-final")
	if !decision.Allowed {
		t.Errorf("Expected final request to be allowed")
	}
}

func TestLimiterWithCustomTimeFunction(t *testing.T) {
	var currentTime time.Time
	config := Config{
		Rate:            10.0,
		Capacity:        100,
		MaxEntries:      1000,
		CleanupInterval: time.Millisecond * 100,
		MaxIdle:         time.Second * 5,
		Shards:          4,
		Now:             func() time.Time { return currentTime },
	}

	limiter := NewLimiter(config)
	defer limiter.Stop()

	// Set initial time
	currentTime = time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	decision := limiter.Allow("custom-time-key")
	if !decision.Allowed {
		t.Errorf("Expected request to be allowed with custom time")
	}

	// Advance time
	currentTime = currentTime.Add(time.Second)
	decision = limiter.Allow("custom-time-key-2")
	if !decision.Allowed {
		t.Errorf("Expected second request to be allowed after time advance")
	}
}
