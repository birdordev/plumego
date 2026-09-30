package contract

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkWriteResponse(b *testing.B) {
	data := map[string]any{"status": "ok"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		_ = WriteResponse(w, r, http.StatusOK, data, nil)
	}
}

func BenchmarkWriteResponseWithMeta(b *testing.B) {
	data := map[string]any{"status": "ok"}
	meta := map[string]any{"page": 1, "total": 100}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		_ = WriteResponse(w, r, http.StatusOK, data, meta)
	}
}

func BenchmarkWriteError(b *testing.B) {
	err := NewErrorBuilder().
		Type(TypeBadRequest).
		Message("invalid input").
		Build()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/test", nil)
		_ = WriteError(w, r, err)
	}
}

func BenchmarkWriteErrorWithDetail(b *testing.B) {
	err := NewErrorBuilder().
		Type(TypeValidation).
		Message("validation failed").
		Detail("field", "name is required").
		Build()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader([]byte(`{}`)))
		_ = WriteError(w, r, err)
	}
}

func BenchmarkWriteJSONDirect(b *testing.B) {
	type payload struct {
		Status string `json:"status"`
	}
	p := payload{Status: "ok"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		_ = writeJSON(w, http.StatusOK, p)
	}
}
