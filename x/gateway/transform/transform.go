// Package transform provides request/response transformation middleware
//
// This package allows modification of HTTP requests and responses without
// protocol-specific knowledge. Transformations can include:
//   - Header manipulation (add, remove, rename)
//   - JSON field renaming and restructuring
//   - Body content modification
//   - Query parameter manipulation
//
// Example usage:
//
//	import (
//		"github.com/spcent/plumego/core"
//		"github.com/spcent/plumego/x/gateway/transform"
//	)
//
//	cfg := core.DefaultConfig()
//	app := core.New(cfg, core.AppDependencies{})
//
//	// Simple transformations
//	if err := app.Use(transform.Middleware(transform.Config{
//		RequestTransformers: []transform.RequestTransformer{
//			transform.AddRequestHeader("X-Custom-Header", "value"),
//			transform.RemoveRequestHeader("X-Internal-Header"),
//		},
//		ResponseTransformers: []transform.ResponseTransformer{
//			transform.AddResponseHeader("X-API-Version", "2"),
//		},
//	})); err != nil {
//		panic(err)
//	}
package transform

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spcent/plumego/contract"
	"github.com/spcent/plumego/internal/httputil"
)

// maxTransformBodyBytes bounds request and response bodies buffered for
// transformation. Without a limit a hostile client or upstream could force
// unbounded memory usage.
const maxTransformBodyBytes = 16 << 20 // 16 MiB

// ErrBodyTooLarge is returned when a request or response body exceeds
// maxTransformBodyBytes.
var ErrBodyTooLarge = errors.New("transform: body exceeds max transform size")

// readBounded reads at most maxTransformBodyBytes from r and fails with
// ErrBodyTooLarge when the source is larger, so truncation is never silent.
func readBounded(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxTransformBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxTransformBodyBytes {
		return nil, ErrBodyTooLarge
	}
	return body, nil
}

// CodeTransformFailed is the canonical x/gateway transform error code.
const CodeTransformFailed = "transform_failed"

// RequestTransformer modifies an HTTP request
type RequestTransformer func(*http.Request) error

// ResponseTransformer modifies an HTTP response
type ResponseTransformer func(*http.Response) error

// Config holds transformation middleware configuration
type Config struct {
	// RequestTransformers are applied to incoming requests
	RequestTransformers []RequestTransformer

	// ResponseTransformers are applied to outgoing responses
	ResponseTransformers []ResponseTransformer

	// OnError is called when transformation fails (optional)
	OnError func(err error)
}

// Middleware creates a transformation middleware
func Middleware(config Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Apply request transformers
			for _, transformer := range config.RequestTransformers {
				if err := transformer(r); err != nil {
					if config.OnError != nil {
						config.OnError(err)
					}
					_ = contract.WriteError(w, r, contract.NewErrorBuilder().
						Type(contract.TypeValidation).
						Message("request transformation failed").
						Build())
					return
				}
			}

			// If no response transformers, just call next
			if len(config.ResponseTransformers) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// Create response recorder for transformation
			recorder := httputil.NewBufferedResponseRecorder(w)

			// Call next handler
			next.ServeHTTP(recorder, r)

			// Create http.Response for transformation
			resp := &http.Response{
				StatusCode: recorder.StatusCode(),
				Header:     recorder.Header(),
				Body:       io.NopCloser(bytes.NewReader(recorder.Body())),
			}

			// Apply response transformers
			for _, transformer := range config.ResponseTransformers {
				if err := transformer(resp); err != nil {
					if config.OnError != nil {
						config.OnError(err)
					}
					_ = contract.WriteError(w, r, contract.NewErrorBuilder().
						Type(contract.TypeInternal).
						Message("response transformation failed").
						Build())
					return
				}
			}

			// Read transformed body
			var transformedBody []byte
			if resp.Body != nil {
				var err error
				transformedBody, err = readBounded(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					if config.OnError != nil {
						config.OnError(err)
					}
					_ = contract.WriteError(w, r, contract.NewErrorBuilder().
						Type(contract.TypeInternal).
						Message("response transformation failed").
						Build())
					return
				}
			}

			// SECURITY NOTE: The transformedBody contains response data that has been
			// processed by the Transform function. This middleware does not inject user
			// input into HTML - it applies transformations to existing responses.
			// XSS protection should be implemented in handlers that generate HTML using utils/html.go.
			// Write transformed response
			copyResponseHeaders(w.Header(), resp.Header)
			if len(transformedBody) > 0 {
				w.Header().Set("Content-Length", strconv.Itoa(len(transformedBody)))
			} else {
				w.Header().Del("Content-Length")
			}
			httputil.EnsureNoSniff(w.Header())
			w.WriteHeader(resp.StatusCode)
			if len(transformedBody) > 0 {
				_, _ = httputil.SafeWrite(w, transformedBody)
			}
		})
	}
}

// ========================================
// Request Transformers
// ========================================

// AddRequestHeader adds a header to the request
func AddRequestHeader(key, value string) RequestTransformer {
	return func(r *http.Request) error {
		r.Header.Set(key, value)
		return nil
	}
}

// RemoveRequestHeader removes a header from the request
func RemoveRequestHeader(key string) RequestTransformer {
	return func(r *http.Request) error {
		r.Header.Del(key)
		return nil
	}
}

// RenameRequestHeader renames a request header
func RenameRequestHeader(from, to string) RequestTransformer {
	return func(r *http.Request) error {
		if value := r.Header.Get(from); value != "" {
			r.Header.Set(to, value)
			r.Header.Del(from)
		}
		return nil
	}
}

// SetRequestMethod changes the HTTP method
func SetRequestMethod(method string) RequestTransformer {
	return func(r *http.Request) error {
		r.Method = method
		return nil
	}
}

// AddQueryParam adds a query parameter to the request
func AddQueryParam(key, value string) RequestTransformer {
	return func(r *http.Request) error {
		q := r.URL.Query()
		q.Add(key, value)
		r.URL.RawQuery = q.Encode()
		return nil
	}
}

// RemoveQueryParam removes a query parameter from the request
func RemoveQueryParam(key string) RequestTransformer {
	return func(r *http.Request) error {
		q := r.URL.Query()
		q.Del(key)
		r.URL.RawQuery = q.Encode()
		return nil
	}
}

func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	lower := strings.ToLower(contentType)
	return strings.Contains(lower, "application/json") || strings.Contains(lower, "+json")
}

func decodeJSONObject(body []byte) (map[string]any, bool) {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, false
	}
	return data, true
}

func setRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

func setResponseBody(r *http.Response, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

// RenameJSONRequestField renames a JSON field in request body
func RenameJSONRequestField(from, to string) RequestTransformer {
	return func(r *http.Request) error {
		// Only process JSON content
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			return nil
		}

		// Read body
		body, err := readBounded(r.Body)
		if err != nil {
			return err
		}
		_ = r.Body.Close()

		// Parse JSON
		data, ok := decodeJSONObject(body)
		if !ok {
			// Not JSON object, restore body
			r.Body = io.NopCloser(bytes.NewReader(body))
			return nil
		}

		// Rename field
		if val, exists := data[from]; exists {
			data[to] = val
			delete(data, from)
		}

		// Marshal back to JSON
		newBody, err := json.Marshal(data)
		if err != nil {
			return err
		}

		// Update request
		setRequestBody(r, newBody)

		return nil
	}
}

// ModifyJSONRequest applies a custom function to modify JSON request body
func ModifyJSONRequest(modifier func(map[string]any) error) RequestTransformer {
	return func(r *http.Request) error {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			return nil
		}

		body, err := readBounded(r.Body)
		if err != nil {
			return err
		}
		_ = r.Body.Close()

		data, ok := decodeJSONObject(body)
		if !ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			return nil
		}

		// Apply modifier
		if err := modifier(data); err != nil {
			return err
		}

		newBody, err := json.Marshal(data)
		if err != nil {
			return err
		}

		setRequestBody(r, newBody)

		return nil
	}
}

// ========================================
// Response Transformers
// ========================================

// AddResponseHeader adds a header to the response
func AddResponseHeader(key, value string) ResponseTransformer {
	return func(r *http.Response) error {
		r.Header.Set(key, value)
		return nil
	}
}

// RemoveResponseHeader removes a header from the response
func RemoveResponseHeader(key string) ResponseTransformer {
	return func(r *http.Response) error {
		r.Header.Del(key)
		return nil
	}
}

// RenameResponseHeader renames a response header
func RenameResponseHeader(from, to string) ResponseTransformer {
	return func(r *http.Response) error {
		if value := r.Header.Get(from); value != "" {
			r.Header.Set(to, value)
			r.Header.Del(from)
		}
		return nil
	}
}

// RenameJSONResponseField renames a JSON field in response body
func RenameJSONResponseField(from, to string) ResponseTransformer {
	return func(r *http.Response) error {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			return nil
		}

		body, err := readBounded(r.Body)
		if err != nil {
			return err
		}
		_ = r.Body.Close()

		data, ok := decodeJSONObject(body)
		if !ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			return nil
		}

		// Rename field
		if val, exists := data[from]; exists {
			data[to] = val
			delete(data, from)
		}

		newBody, err := json.Marshal(data)
		if err != nil {
			return err
		}

		setResponseBody(r, newBody)

		return nil
	}
}

// ModifyJSONResponse applies a custom function to modify JSON response body
func ModifyJSONResponse(modifier func(map[string]any) error) ResponseTransformer {
	return func(r *http.Response) error {
		if !isJSONContentType(r.Header.Get("Content-Type")) {
			return nil
		}

		body, err := readBounded(r.Body)
		if err != nil {
			return err
		}
		_ = r.Body.Close()

		data, ok := decodeJSONObject(body)
		if !ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			return nil
		}

		if err := modifier(data); err != nil {
			return err
		}

		newBody, err := json.Marshal(data)
		if err != nil {
			return err
		}

		setResponseBody(r, newBody)

		return nil
	}
}

// SetResponseStatus changes the response status code
func SetResponseStatus(statusCode int) ResponseTransformer {
	return func(r *http.Response) error {
		r.StatusCode = statusCode
		return nil
	}
}

// Chain combines multiple transformers into one
func ChainRequest(transformers ...RequestTransformer) RequestTransformer {
	return func(r *http.Request) error {
		for _, transformer := range transformers {
			if err := transformer(r); err != nil {
				return err
			}
		}
		return nil
	}
}

// ChainResponse combines multiple response transformers into one
func ChainResponse(transformers ...ResponseTransformer) ResponseTransformer {
	return func(r *http.Response) error {
		for _, transformer := range transformers {
			if err := transformer(r); err != nil {
				return err
			}
		}
		return nil
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
