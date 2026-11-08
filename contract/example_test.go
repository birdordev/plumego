package contract_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/contract"
)

func ExampleWriteError() {
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req = req.WithContext(contract.WithRequestID(req.Context(), "req-123"))
	rec := httptest.NewRecorder()

	err := contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message("user not found").
		Build()

	_ = contract.WriteError(rec, req, err)

	fmt.Println(rec.Code)
	fmt.Print(rec.Body.String())

	// Output:
	// 404
	// {"error":{"code":"RESOURCE_NOT_FOUND","message":"user not found","category":"client_error","type":"resource_not_found"},"request_id":"req-123"}
}

// ExampleAsAPIError shows how a service layer can carry an APIError through the
// Go error chain and how a handler extracts it without re-constructing the error.
func ExampleAsAPIError() {
	// Simulate a repository returning a typed APIError wrapped in a service error.
	repoErr := contract.NewErrorBuilder().
		Type(contract.TypeNotFound).
		Message("record not found").
		Build()
	serviceErr := fmt.Errorf("repo.Find: %w", repoErr)

	// Handler extracts the APIError from the chain.
	if apiErr, ok := contract.AsAPIError(serviceErr); ok {
		fmt.Println(apiErr.Status())
		fmt.Println(apiErr.Code())
	}

	// Also demonstrate Wrap + errors.Is for diagnostic cause access.
	dbErr := errors.New("connection reset")
	wrapped := contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Wrap(dbErr).
		Build()
	fmt.Println(errors.Is(wrapped, dbErr))

	// Output:
	// 404
	// RESOURCE_NOT_FOUND
	// true
}

func ExampleWriteResponse() {
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req = req.WithContext(contract.WithRequestID(req.Context(), "req-123"))
	rec := httptest.NewRecorder()

	_ = contract.WriteResponse(rec, req, http.StatusOK, map[string]string{
		"id": "42",
	}, map[string]any{
		"count": 1,
	})

	fmt.Println(rec.Code)
	fmt.Print(rec.Body.String())

	// Output:
	// 200
	// {"data":{"id":"42"},"meta":{"count":1},"request_id":"req-123"}
}
