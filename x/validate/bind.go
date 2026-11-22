package validate

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// BindJSON decodes one JSON value into T without validation or an EOF check.
// A request with no body returns io.EOF.
// The caller owns any request body size limit. On decoding error the
// returned value may be partially populated; only use it when err is nil.
func BindJSON[T any](r *http.Request) (T, error) {
	var out T
	if r.Body == nil {
		return out, io.EOF
	}
	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// Bind decodes one JSON value using BindJSON and passes the decoded T to v.
// Choose a value or pointer T to match the validator. JSON null passes a nil
// pointer when T is a pointer type. A nil v skips validation.
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

// BindStrict 解码单个有大小上限的 JSON 请求，再调用可选校验器。
// 解码失败返回传输层 API 错误。
func BindStrict[T any](r *http.Request, v Validator, opts contract.JSONRequestOptions) (T, error) {
	var out T
	if err := contract.DecodeJSONRequest(r, &out, opts); err != nil {
		return out, err
	}
	if v != nil {
		if err := v.Validate(out); err != nil {
			return out, newValidationError(err)
		}
	}
	return out, nil
}
