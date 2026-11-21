package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorWithBuilder(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/test", nil)

	err := WriteError(w, r, NewErrorBuilder().
		Type(TypeBadRequest).
		Code(CodeInvalidRequest).
		Message("bad request").
		Detail("field", "name").
		Build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if response.Error.Code != CodeInvalidRequest {
		t.Fatalf("expected code %s, got %q", CodeInvalidRequest, response.Error.Code)
	}
	if response.Error.Category != CategoryClient {
		t.Fatalf("expected category %q, got %q", CategoryClient, response.Error.Category)
	}
}

func TestStatusCategoryFallback(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		expectedCategory ErrorCategory
	}{
		{"bad request", http.StatusBadRequest, CategoryClient},
		{"unauthorized", http.StatusUnauthorized, CategoryAuth},
		{"forbidden", http.StatusForbidden, CategoryAuth},
		{"not found", http.StatusNotFound, CategoryClient},
		{"too many requests", http.StatusTooManyRequests, CategoryRateLimit},
		{"request timeout", http.StatusRequestTimeout, CategoryTimeout},
		{"gateway timeout", http.StatusGatewayTimeout, CategoryTimeout},
		{"internal server error", http.StatusInternalServerError, CategoryServer},
		{"unprocessable entity fallback", http.StatusUnprocessableEntity, CategoryClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := categoryForStatus(tt.status); got != tt.expectedCategory {
				t.Fatalf("status %d: expected category %q, got %q", tt.status, tt.expectedCategory, got)
			}
		})
	}
}

func TestRouteStateSnapshotKeepsCaseDistinctParameters(t *testing.T) {
	state := GetRouteState()
	state.AddParam("Name", "upper")
	state.AddParam("name", "lower")
	ctx := WithRouteState(t.Context(), state)
	PutRouteState(state)
	snapshot := RequestContextFromContext(ctx)
	if RequestParamFromContext(ctx, "Name") != "upper" || RequestParamFromContext(ctx, "name") != "lower" ||
		snapshot.Params["Name"] != "upper" || snapshot.Params["name"] != "lower" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
