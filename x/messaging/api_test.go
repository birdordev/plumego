package messaging

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
	"github.com/birdordev/plumego/x/messaging/mq"
)

func assertSafePublicErrorMessage(t *testing.T, message string) {
	t.Helper()

	if strings.Contains(message, "messaging:") {
		t.Fatalf("message exposes raw error text: %q", message)
	}
}

func TestClassifyServiceError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "provider error",
			err:        ErrProviderFailure,
			wantStatus: http.StatusBadGateway,
			wantCode:   "PROVIDER_ERROR",
			wantMsg:    "provider error",
		},
		{
			name:       "quota exceeded",
			err:        ErrQuotaExceeded,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "QUOTA_EXCEEDED",
			wantMsg:    "quota exceeded",
		},
		{
			name:       "duplicate task",
			err:        mq.ErrDuplicateTask,
			wantStatus: http.StatusConflict,
			wantCode:   "DUPLICATE_MESSAGE",
			wantMsg:    "duplicate message",
		},
		{
			name:       "task expired",
			err:        mq.ErrTaskExpired,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   contract.CodeInvalidRequest,
			wantMsg:    "task expired",
		},
		{
			name:       "validation",
			err:        ErrMissingID,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "VALIDATION_ERROR",
			wantMsg:    "message validation failed",
		},
		{
			name:       "mq invalid config",
			err:        mq.ErrInvalidConfig,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "VALIDATION_ERROR",
			wantMsg:    "message validation failed",
		},
		{
			name:       "not initialized",
			err:        mq.ErrNotInitialized,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "SERVICE_UNAVAILABLE",
			wantMsg:    "messaging service unavailable",
		},
		{
			name:       "context timeout",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   contract.CodeGatewayTimeout,
			wantMsg:    "request timed out",
		},
		{
			name:       "unknown",
			err:        errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "SEND_ERROR",
			wantMsg:    "send failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr := classifyServiceError(tt.err)
			if apiErr.Status() != tt.wantStatus {
				t.Fatalf("status=%d, want %d", apiErr.Status(), tt.wantStatus)
			}
			if apiErr.Code() != tt.wantCode {
				t.Fatalf("code=%s, want %s", apiErr.Code(), tt.wantCode)
			}
			if apiErr.Message() != tt.wantMsg {
				t.Fatalf("message=%q, want %q", apiErr.Message(), tt.wantMsg)
			}
		})
	}
}

func TestHandleSendMissingRequiredFieldsUsesSafeError(t *testing.T) {
	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, "/messages/send", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	svc.HandleSend(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusUnprocessableEntity)
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
	if resp.Error.Message != "message validation failed" {
		t.Fatalf("message=%q, want %q", resp.Error.Message, "message validation failed")
	}
	assertSafePublicErrorMessage(t, resp.Error.Message)
}

func TestHandleBatchSendEmptyRequestsUsesSafeError(t *testing.T) {
	svc := New(Config{})
	req := httptest.NewRequest(http.MethodPost, "/messages/batch", bytes.NewBufferString(`{"requests":[]}`))
	rec := httptest.NewRecorder()

	svc.HandleBatchSend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusBadRequest)
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
	if resp.Error.Code != CodeEmptyBatch {
		t.Fatalf("code=%s, want %s", resp.Error.Code, CodeEmptyBatch)
	}
	if resp.Error.Message != "requests array is empty" {
		t.Fatalf("message=%q, want %q", resp.Error.Message, "requests array is empty")
	}
	assertSafePublicErrorMessage(t, resp.Error.Message)
}

func TestHandleSendAcceptedUsesTypedResponse(t *testing.T) {
	svc := newTestService(&mockSMS{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/messages/send", bytes.NewBufferString(`{
		"id":"msg-1",
		"channel":"sms",
		"to":"+1234567890",
		"body":"hello"
	}`))
	rec := httptest.NewRecorder()

	svc.HandleSend(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	resp := decodeMessagingAPIData[sendAcceptedResponse](t, rec)
	if resp.ID != "msg-1" || resp.Status != messageStatusQueued {
		t.Fatalf("unexpected send response: %+v", resp)
	}
}

func TestHandleListReceiptsUsesTypedResponse(t *testing.T) {
	svc := New(Config{})
	if err := svc.receipts.Save(Receipt{
		ID:      "receipt-1",
		Channel: ChannelSMS,
		To:      "+1234567890",
		Status:  messageStatusQueued,
	}); err != nil {
		t.Fatalf("save receipt: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/messages/receipts", nil)
	rec := httptest.NewRecorder()

	svc.HandleListReceipts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	resp := decodeMessagingAPIData[receiptListResponse](t, rec)
	if resp.Count != 1 || len(resp.Receipts) != 1 {
		t.Fatalf("unexpected receipt list response: %+v", resp)
	}
	if resp.Receipts[0].ID != "receipt-1" {
		t.Fatalf("receipt id=%q, want receipt-1", resp.Receipts[0].ID)
	}
}

func TestHandleChannelHealthUsesTypedResponse(t *testing.T) {
	svc := newTestService(&mockSMS{}, &mockEmail{})
	req := httptest.NewRequest(http.MethodGet, "/messages/channels", nil)
	rec := httptest.NewRecorder()

	svc.HandleChannelHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	resp := decodeMessagingAPIData[channelHealthResponse](t, rec)
	if len(resp.Channels) != 2 {
		t.Fatalf("channels=%d, want 2: %+v", len(resp.Channels), resp.Channels)
	}
}

func decodeMessagingAPIData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if got := rec.Header().Get(contract.HeaderContentType); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

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
