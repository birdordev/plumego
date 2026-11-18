package validate

import (
	"encoding/json"
	"net/http"
)

// BindJSON decodes one JSON value into T without validation or an EOF check.
// The caller owns any request body size limit.
func BindJSON[T any](r *http.Request) (T, error) {
	var out T
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// Bind decodes one JSON value using BindJSON, then validates the decoded value.
func Bind[T any](r *http.Request, v Validator) (T, error) {
	out, err := BindJSON[T](r)
	if err != nil {
		return out, err
	}
	if v == nil {
		return out, nil
	}
	if err := v.Validate(out); err != nil {
		return out, newValidationError(err)
	}
	return out, nil
}
