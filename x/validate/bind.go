package validate

import (
	"encoding/json"
	"net/http"
)

// BindJSON decodes one JSON value into T without validation or an EOF check.
// The caller owns any request body size limit. On decoding error the
// returned value may be partially populated; only use it when err is nil.
func BindJSON[T any](r *http.Request) (T, error) {
	var out T
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// Bind decodes one JSON value using BindJSON, then validates the decoded value.
// It returns the partially populated value on a decoding error and the decoded
// value on a validation error. Only use the result when err is nil.
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
