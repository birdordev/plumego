package validate

import (
	"errors"
	"mime"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/contract"
)

// BindQuery parses query values and invokes the caller's validation function.
func BindQuery(r *http.Request, bind func(url.Values) error) error {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return bindingError(contract.TypeBadRequest, "invalid query", err)
	}
	return bindValues(values, bind)
}

// BindForm parses a bounded URL-encoded body without merging the query string.
func BindForm(w http.ResponseWriter, r *http.Request, maxBytes int64, bind func(url.Values) error) error {
	if maxBytes <= 0 {
		return bindingError(contract.TypeInternal, "invalid form body limit", nil)
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		return bindingError(contract.TypeUnsupportedMediaType, "expected a URL-encoded form", err)
	}
	if r.ContentLength > maxBytes {
		return bindingError(contract.TypePayloadTooLarge, "form body too large", nil)
	}
	copy := *r
	body := r.Body
	if body == nil {
		body = http.NoBody
	}
	copy.Body = http.MaxBytesReader(w, body, maxBytes)
	// ParseForm handles bodies for POST, PUT and PATCH. Binding operates on
	// the body alone and does not depend on the outer request method.
	copy.Method = http.MethodPost
	copy.PostForm, copy.Form = nil, nil
	address := *r.URL
	address.RawQuery = ""
	copy.URL = &address
	if err := copy.ParseForm(); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return bindingError(contract.TypePayloadTooLarge, "form body too large", err)
		}
		return bindingError(contract.TypeBadRequest, "invalid form", err)
	}
	return bindValues(copy.PostForm, bind)
}

func bindValues(values url.Values, bind func(url.Values) error) error {
	if bind != nil {
		if err := bind(values); err != nil {
			return newValidationError(err)
		}
	}
	return nil
}

func bindingError(kind contract.ErrorType, message string, cause error) error {
	return contract.NewErrorBuilder().Type(kind).Message(message).Wrap(cause).Build()
}
