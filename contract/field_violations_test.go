package contract_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type invalidFixtureField struct{}

func (invalidFixtureField) Error() string { return "private fixture value" }
func (invalidFixtureField) FieldViolations() []contract.FieldViolation {
	return []contract.FieldViolation{
		{Source: "body", Field: "name", Code: "REQUIRED"},
		{Source: "unknown", Field: "fixture", Code: "INVALID"},
		{Source: "query", Field: "raw fixture value", Code: "INVALID"},
	}
}

func TestFieldViolationsSerializeOnlyDeclaredDetails(t *testing.T) {
	err := fmt.Errorf("invalid data: %w", invalidFixtureField{})
	apiErr := contract.NewErrorBuilder().Type(contract.TypeValidation).
		Message("validation failed").FieldViolations(err).Wrap(err).Build()
	w := httptest.NewRecorder()
	if err := contract.WriteError(w, httptest.NewRequest(http.MethodPost, "/", nil), apiErr); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"fields":[{"source":"body","field":"name","code":"REQUIRED"}]`) ||
		strings.Contains(body, "fixture") {
		t.Fatalf("unexpected error details: %s", body)
	}
}
