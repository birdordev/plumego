package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging"
)

func TestMessagingHandlerPublishResponse(t *testing.T) {
	broker := messaging.NewInProcBroker()
	defer broker.Close()

	handler := MessagingHandler{Broker: broker, Logger: nil}
	req := httptest.NewRequest(http.MethodPost, "/events/publish", strings.NewReader(`{"topic":"demo","payload":"hello"}`))
	rec := httptest.NewRecorder()

	handler.Publish(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	resp := decodeMessagingData[publishResponse](t, rec)
	if !resp.OK || resp.Topic != "demo" || resp.Timestamp == "" {
		t.Fatalf("unexpected publish response: %+v", resp)
	}
}

func decodeMessagingData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}
