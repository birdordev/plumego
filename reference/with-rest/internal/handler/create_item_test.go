package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"with-rest/internal/validation/playground"
)

func discardLogger() plumelog.StructuredLogger {
	return plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard})
}

func TestCreateItemValidRequestPasses(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(`{"name":"widget"}`))

	NewCreateItem(playground.NewValidator(), discardLogger()).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body struct {
		Data CreateItemResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Name != "widget" {
		t.Fatalf("response name = %q, want widget", body.Data.Name)
	}
}

func TestCreateItemMissingRequiredFieldReturnsStructuredError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(`{}`))

	NewCreateItem(playground.NewValidator(), discardLogger()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Details struct {
				Fields []playground.FieldError `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != contract.CodeValidationError {
		t.Fatalf("error code = %q, want %q", body.Error.Code, contract.CodeValidationError)
	}
	if body.Error.Type != string(contract.TypeBadRequest) {
		t.Fatalf("error type = %q, want %q", body.Error.Type, contract.TypeBadRequest)
	}
	if len(body.Error.Details.Fields) != 1 {
		t.Fatalf("fields = %#v, want one field error", body.Error.Details.Fields)
	}
	field := body.Error.Details.Fields[0]
	if field.Field != "Name" || field.Tag != "required" {
		t.Fatalf("field error = %#v, want Name required", field)
	}
}

func TestCreateItemMalformedJSONReturnsBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/items", strings.NewReader(`{"name":`))

	NewCreateItem(playground.NewValidator(), discardLogger()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != contract.CodeInvalidJSON {
		t.Fatalf("error code = %q, want %q", body.Error.Code, contract.CodeInvalidJSON)
	}
	if body.Error.Type != string(contract.TypeBadRequest) {
		t.Fatalf("error type = %q, want %q", body.Error.Type, contract.TypeBadRequest)
	}
}
