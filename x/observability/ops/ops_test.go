package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
)

type summaryEnvelope struct {
	Data struct {
		BasePath string `json:"base_path"`
		Auth     struct {
			Required bool `json:"required"`
			Enabled  bool `json:"enabled"`
		} `json:"auth"`
		Features struct {
			QueueStats bool `json:"queue_stats"`
		} `json:"features"`
	} `json:"data"`
}

type queueStatsResponse struct {
	Data struct {
		Queues []QueueStats `json:"queues"`
	} `json:"data"`
}

func TestOpsQueueStatsSingle(t *testing.T) {
	r := router.NewRouter()
	handler := New(Options{
		Enabled: true,
		Auth:    AuthConfig{AllowInsecure: true},
		Hooks: Hooks{
			QueueStats: func(ctx context.Context, queue string) (QueueStats, error) {
				return QueueStats{Queue: queue, Queued: 3}, nil
			},
		},
	})

	_ = handler.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ops/queue?queue=primary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp queueStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Data.Queues) != 1 {
		t.Fatalf("expected 1 queue, got %d", len(resp.Data.Queues))
	}
	if resp.Data.Queues[0].Queue != "primary" {
		t.Fatalf("expected queue primary, got %q", resp.Data.Queues[0].Queue)
	}
}

func TestOpsSummaryResponseShape(t *testing.T) {
	r := router.NewRouter()
	handler := New(Options{
		Enabled: true,
		Auth:    AuthConfig{AllowInsecure: true},
		Hooks: Hooks{
			QueueStats: func(ctx context.Context, queue string) (QueueStats, error) {
				return QueueStats{Queue: queue}, nil
			},
		},
	})
	_ = handler.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ops", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp summaryEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.BasePath != "/ops" {
		t.Fatalf("base_path = %q, want /ops", resp.Data.BasePath)
	}
	if resp.Data.Auth.Required {
		t.Fatal("auth.required = true, want false for insecure test handler")
	}
	if !resp.Data.Features.QueueStats {
		t.Fatal("features.queue_stats = false, want true")
	}
}

func TestOpsAuthRequired(t *testing.T) {
	r := router.NewRouter()
	handler := New(Options{
		Enabled: true,
		Hooks: Hooks{
			QueueStats: func(ctx context.Context, queue string) (QueueStats, error) {
				return QueueStats{Queue: queue}, nil
			},
		},
	})

	_ = handler.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ops/queue?queue=primary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}

func TestOpsQueueStatsMissingQueueUsesRequiredQueryError(t *testing.T) {
	r := router.NewRouter()
	handler := New(Options{
		Enabled: true,
		Auth:    AuthConfig{AllowInsecure: true},
		Hooks: Hooks{
			QueueStats: func(ctx context.Context, queue string) (QueueStats, error) {
				return QueueStats{Queue: queue}, nil
			},
		},
	})
	_ = handler.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ops/queue", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != contract.CodeValidationError {
		t.Fatalf("code=%s, want %s", resp.Error.Code, contract.CodeValidationError)
	}
	if got := resp.Error.Details["field"]; got != "queue" {
		t.Fatalf("field detail=%v, want queue", got)
	}
}

func TestOpsHookErrorLogDoesNotLeakRawError(t *testing.T) {
	var logBuf bytes.Buffer
	r := router.NewRouter()
	handler := New(Options{
		Enabled: true,
		Auth:    AuthConfig{AllowInsecure: true},
		Logger: plumelog.NewLogger(plumelog.LoggerConfig{
			Format:      plumelog.LoggerFormatJSON,
			Output:      &logBuf,
			ErrorOutput: &logBuf,
		}),
		Hooks: Hooks{
			QueueStats: func(ctx context.Context, queue string) (QueueStats, error) {
				return QueueStats{}, errors.New("backend password=secret for " + queue)
			},
		},
	})
	_ = handler.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ops/queue?queue=primary", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "password=secret") {
		t.Fatalf("response leaked hook error: %s", rec.Body.String())
	}
	logOutput := logBuf.String()
	if strings.Contains(logOutput, "password=secret") || strings.Contains(logOutput, "backend password") {
		t.Fatalf("log leaked hook error: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"error_type"`) {
		t.Fatalf("expected safe error_type field in log, got: %s", logOutput)
	}
}

func TestOpsNotConfiguredErrorsUseStableCodes(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		wantCode string
	}{
		{
			name:     "queue stats",
			method:   http.MethodGet,
			path:     "/ops/queue",
			wantCode: codeQueueStatsNotConfigured,
		},
		{
			name:     "queue replay",
			method:   http.MethodPost,
			path:     "/ops/queue/replay",
			wantCode: codeQueueReplayNotConfigured,
		},
		{
			name:     "receipt lookup",
			method:   http.MethodGet,
			path:     "/ops/receipts",
			wantCode: codeReceiptLookupNotConfigured,
		},
		{
			name:     "channel health",
			method:   http.MethodGet,
			path:     "/ops/channels",
			wantCode: codeChannelHealthNotConfigured,
		},
		{
			name:     "tenant quota",
			method:   http.MethodGet,
			path:     "/ops/tenants/quota",
			wantCode: codeTenantQuotaNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.NewRouter()
			handler := New(Options{
				Enabled: true,
				Auth:    AuthConfig{AllowInsecure: true},
			})
			_ = handler.RegisterRoutes(r)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("expected status 501, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp struct {
				Error struct {
					Code     string                 `json:"code"`
					Message  string                 `json:"message"`
					Category contract.ErrorCategory `json:"category"`
					Type     contract.ErrorType     `json:"type,omitempty"`
					Details  map[string]any         `json:"details,omitempty"`
				} `json:"error"`
				RequestID string `json:"request_id,omitempty"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Error.Code != tt.wantCode {
				t.Fatalf("code=%s, want %s", resp.Error.Code, tt.wantCode)
			}
		})
	}
}
