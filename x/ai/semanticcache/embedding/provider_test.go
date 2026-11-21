package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestReadResponseBody(t *testing.T) {
	atLimit, err := readResponseBody(strings.NewReader(strings.Repeat("a", 8)))
	if err != nil {
		t.Fatalf("readResponseBody() error = %v", err)
	}
	if len(atLimit) != 8 {
		t.Errorf("readResponseBody() len = %d, want 8", len(atLimit))
	}
}

func TestReadResponseBody_TooLarge(t *testing.T) {
	// Streaming a body larger than maxResponseBytes must fail with
	// ErrResponseTooLarge rather than being read into memory unbounded.
	body := strings.NewReader(strings.Repeat("a", int(maxResponseBytes)+1))
	_, err := readResponseBody(body)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("readResponseBody() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestOpenAIProvider_OversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := make([]byte, 1<<20)
		for i := int64(0); i <= maxResponseBytes/int64(len(chunk)); i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	p, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	p.maxRetries = 0

	_, err = p.Generate(context.Background(), "hello")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Generate() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestCohereProvider_OversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := make([]byte, 1<<20)
		for i := int64(0); i <= maxResponseBytes/int64(len(chunk)); i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	p, err := NewCohereProvider(CohereConfig{APIKey: "test-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewCohereProvider() error = %v", err)
	}
	p.maxRetries = 0

	_, err = p.Generate(context.Background(), "hello")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Generate() error = %v, want ErrResponseTooLarge", err)
	}
}

func TestCohereProvider_DefaultBaseURL(t *testing.T) {
	p, err := NewCohereProvider(CohereConfig{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewCohereProvider() error = %v", err)
	}
	if p.baseURL != "https://api.cohere.com/v1" {
		t.Errorf("default baseURL = %q, want https://api.cohere.com/v1", p.baseURL)
	}
}

func TestOpenAIProvider_APIErrorSurfacesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key","type":"auth","code":"401"}}`)
	}))
	defer srv.Close()

	p, err := NewOpenAIProvider(OpenAIConfig{APIKey: "bad-key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	p.maxRetries = 0

	_, err = p.Generate(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("Generate() error = %v, want it to surface the provider message", err)
	}
}

type statsEmbeddingFixture struct {
	Provider
	generate func(context.Context, string) (*Embedding, error)
	batch    func(context.Context, []string) ([]*Embedding, error)
}

func (f statsEmbeddingFixture) Generate(ctx context.Context, text string) (*Embedding, error) {
	return f.generate(ctx, text)
}
func (f statsEmbeddingFixture) GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error) {
	return f.batch(ctx, texts)
}

func TestEmbeddingProviderStatsSnapshotAndReset(t *testing.T) {
	var p *ProviderWithStats
	entered, release := make(chan struct{}), make(chan struct{})
	p = NewProviderWithStats(statsEmbeddingFixture{
		generate: func(_ context.Context, text string) (*Embedding, error) {
			_ = p.Stats()
			if text == "pending" {
				close(entered)
				<-release
			}
			return &Embedding{TokensUsed: 2}, nil
		},
		batch: func(context.Context, []string) ([]*Embedding, error) { return []*Embedding{{TokensUsed: 2}}, nil },
	})
	p.Stats().TotalTokens = 123
	if p.Stats().TotalTokens != 0 {
		t.Fatal("Stats aliases its counters")
	}
	done := make(chan error, 1)
	go func() { _, err := p.Generate(t.Context(), "pending"); done <- err }()
	<-entered
	p.ResetStats()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := p.Stats(); got.TotalEmbeddings != 1 || got.TotalTokens != 2 {
		t.Errorf("post-reset completion=%+v", got)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range 100 {
			if _, err := p.Generate(t.Context(), "ongoing"); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		<-start
		for range 100 {
			if _, err := p.GenerateBatch(t.Context(), []string{"ongoing"}); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Go(func() {
		<-start
		for range 100 {
			p.ResetStats()
			p.Stats().TotalTokens = -1
		}
	})
	close(start)
	wg.Wait()
	p.ResetStats()
	if got := p.Stats(); *got != (Stats{}) {
		t.Errorf("reset left stats=%+v", got)
	}
}

func TestEmbeddingProviderStatsCountsCompletedCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("embedding unavailable")
		result := &Embedding{TokensUsed: 3, Cost: 0.5}
		p := NewProviderWithStats(statsEmbeddingFixture{
			generate: func(_ context.Context, text string) (*Embedding, error) {
				if text == "fail" {
					time.Sleep(2 * time.Second)
					return nil, sentinel
				}
				time.Sleep(4 * time.Second)
				return result, nil
			},
			batch: func(_ context.Context, texts []string) ([]*Embedding, error) {
				if len(texts) == 0 {
					time.Sleep(3 * time.Second)
					return nil, sentinel
				}
				time.Sleep(time.Second)
				return []*Embedding{result, nil}, sentinel
			},
		})
		check := func(want Stats) {
			t.Helper()
			if got := p.Stats(); *got != want {
				t.Errorf("stats=%+v, want %+v", got, want)
			}
		}
		if got, err := p.Generate(t.Context(), "fail"); got != nil || !errors.Is(err, sentinel) {
			t.Errorf("failure=%v, %v", got, err)
		}
		check(Stats{TotalEmbeddings: 1, ErrorRate: 1, AverageLatency: 2 * time.Second})
		if got, err := p.Generate(t.Context(), "success"); got != result || err != nil {
			t.Errorf("success=%v, %v", got, err)
		}
		check(Stats{TotalEmbeddings: 2, TotalTokens: 3, TotalCost: 0.5, ErrorRate: 0.5, AverageLatency: 3 * time.Second})
		got, err := p.GenerateBatch(t.Context(), []string{"partial", "missing"})
		if len(got) != 2 || got[0] != result || got[1] != nil || !errors.Is(err, sentinel) {
			t.Errorf("partial=%v, %v", got, err)
		}
		check(Stats{TotalEmbeddings: 4, TotalTokens: 6, TotalCost: 1, ErrorRate: 2.0 / 3, AverageLatency: 7 * time.Second / 3})
		if _, err := p.GenerateBatch(t.Context(), nil); !errors.Is(err, sentinel) {
			t.Errorf("empty error=%v", err)
		}
		check(Stats{TotalEmbeddings: 4, TotalTokens: 6, TotalCost: 1, ErrorRate: 0.75, AverageLatency: 2500 * time.Millisecond})
		p.ResetStats()
		if _, err := p.GenerateBatch(t.Context(), nil); !errors.Is(err, sentinel) {
			t.Errorf("first empty error=%v", err)
		}
		check(Stats{ErrorRate: 1, AverageLatency: 3 * time.Second})
	})
}
