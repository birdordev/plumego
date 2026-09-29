package embedding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	_, err = p.Generate(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("Generate() error = %v, want it to surface the provider message", err)
	}
}
