package transform

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/birdordev/plumego/contract"
)

type renameResponseFieldPayload struct {
	OldField string `json:"oldField,omitempty"`
	NewField string `json:"newField,omitempty"`
}

type modifyResponsePayload struct {
	Original string `json:"original,omitempty"`
	Modified bool   `json:"modified,omitempty"`
}

type transformErrorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func TestAddRequestHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "value" {
			t.Error("Expected X-Custom header to be added")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			AddRequestHeader("X-Custom", "value"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestRemoveRequestHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Remove-Me") != "" {
			t.Error("Expected X-Remove-Me header to be removed")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			RemoveRequestHeader("X-Remove-Me"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Remove-Me", "should be removed")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestRenameRequestHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Old") != "" {
			t.Error("Expected X-Old header to be removed")
		}
		if r.Header.Get("X-New") != "value" {
			t.Error("Expected X-New header to have value")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			RenameRequestHeader("X-Old", "X-New"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Old", "value")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestAddQueryParam(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("newparam") != "newvalue" {
			t.Error("Expected newparam query parameter to be added")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			AddQueryParam("newparam", "newvalue"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestRemoveQueryParam(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("remove") != "" {
			t.Error("Expected remove query parameter to be removed")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			RemoveQueryParam("remove"),
		},
	})

	req := httptest.NewRequest("GET", "/test?remove=me", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestRenameJSONRequestField(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		if _, exists := data["oldField"]; exists {
			t.Error("Expected oldField to be removed")
		}

		if data["newField"] != "value" {
			t.Error("Expected newField to have value")
		}

		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			RenameJSONRequestField("oldField", "newField"),
		},
	})

	reqBody := `{"oldField": "value"}`
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestModifyJSONRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(body, &data)

		if data["modified"] != true {
			t.Error("Expected modified field to be true")
		}

		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			ModifyJSONRequest(func(data map[string]any) error {
				data["modified"] = true
				return nil
			}),
		},
	})

	reqBody := `{"original": "value"}`
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestAddResponseHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			AddResponseHeader("X-API-Version", "2"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	if w.Header().Get("X-API-Version") != "2" {
		t.Error("Expected X-API-Version header to be added to response")
	}
}

func TestRemoveResponseHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Remove-Me", "value")
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			RemoveResponseHeader("X-Remove-Me"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	if w.Header().Get("X-Remove-Me") != "" {
		t.Error("Expected X-Remove-Me header to be removed from response")
	}
}

func TestRenameJSONResponseFieldUpdatesContentLength(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"old":"value"}`))
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			RenameJSONResponseField("old", "new"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	body := w.Body.Bytes()
	got := w.Header().Get("Content-Length")
	want := strconv.Itoa(len(body))
	if got != want {
		t.Fatalf("expected Content-Length %q, got %q", want, got)
	}
}

func TestRenameResponseHeader(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Old-Header", "value")
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			RenameResponseHeader("X-Old-Header", "X-New-Header"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	if w.Header().Get("X-Old-Header") != "" {
		t.Error("Expected X-Old-Header to be removed")
	}

	if w.Header().Get("X-New-Header") != "value" {
		t.Error("Expected X-New-Header to have value")
	}
}

func TestRenameJSONResponseField(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(renameResponseFieldPayload{OldField: "value"})
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			RenameJSONResponseField("oldField", "newField"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	var data renameResponseFieldPayload
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if data.OldField != "" {
		t.Error("Expected oldField to be removed from response")
	}

	if data.NewField != "value" {
		t.Error("Expected newField to have value in response")
	}
}

func TestModifyJSONResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(modifyResponsePayload{Original: "value"})
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			ModifyJSONResponse(func(data map[string]any) error {
				data["modified"] = true
				return nil
			}),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	var data modifyResponsePayload
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if !data.Modified {
		t.Error("Expected modified field to be true in response")
	}
}

func TestResponseTransformWriteUsesNoSniff(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(modifyResponsePayload{Original: "value"})
	})

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{
			ModifyJSONResponse(func(data map[string]any) error {
				data["modified"] = true
				return nil
			}),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, w.Code)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff header, got %q", got)
	}

	var data modifyResponsePayload
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !data.Modified {
		t.Fatal("expected transformed response body")
	}
}

func TestChainRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Header-1") != "value1" {
			t.Error("Expected X-Header-1 to be added")
		}
		if r.Header.Get("X-Header-2") != "value2" {
			t.Error("Expected X-Header-2 to be added")
		}
		w.WriteHeader(http.StatusOK)
	})

	chained := ChainRequest(
		AddRequestHeader("X-Header-1", "value1"),
		AddRequestHeader("X-Header-2", "value2"),
	)

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{chained},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestChainResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	chained := ChainResponse(
		AddResponseHeader("X-Header-1", "value1"),
		AddResponseHeader("X-Header-2", "value2"),
	)

	middleware := Middleware(Config{
		ResponseTransformers: []ResponseTransformer{chained},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)

	if w.Header().Get("X-Header-1") != "value1" {
		t.Error("Expected X-Header-1 to be added")
	}
	if w.Header().Get("X-Header-2") != "value2" {
		t.Error("Expected X-Header-2 to be added")
	}
}

func TestSetRequestMethod(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected method to be POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			SetRequestMethod("POST"),
		},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestNonJSONBodyNotTransformed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "plain text" {
			t.Error("Expected plain text body to remain unchanged")
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			RenameJSONRequestField("field", "newField"),
		},
	})

	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString("plain text"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	middleware(handler).ServeHTTP(w, req)
}

func TestRequestTransformFailureUsesGatewayTransformCode(t *testing.T) {
	handler := Middleware(Config{
		RequestTransformers: []RequestTransformer{
			func(*http.Request) error { return io.EOF },
		},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var payload transformErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if payload.Error.Code != contract.CodeValidationError {
		t.Fatalf("expected code %q, got %q", contract.CodeValidationError, payload.Error.Code)
	}
}
