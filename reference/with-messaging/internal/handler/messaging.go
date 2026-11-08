// Package handler contains the HTTP handlers for the with-messaging demo.
package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging"
)

// MessagingHandler handles pub/sub event endpoints.
// The Broker dependency is injected explicitly via the struct field.
type MessagingHandler struct {
	Broker *messaging.Broker
	Logger plumelog.StructuredLogger
}

type publishResponse struct {
	OK        bool   `json:"ok"`
	Topic     string `json:"topic"`
	Timestamp string `json:"timestamp"`
}

// Publish accepts a topic and payload and publishes it to the in-process broker.
func (h MessagingHandler) Publish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("invalid JSON body").
			Build()))
		return
	}
	if body.Topic == "" {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("topic is required").
			Build()))
		return
	}

	msg := messaging.BrokerMessage{
		Topic: body.Topic,
		Type:  "manual.publish",
		Time:  time.Now(),
		Data:  body.Payload,
	}
	if err := h.Broker.Publish(body.Topic, msg); err != nil {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to publish event").
			Build()))
		return
	}

	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusAccepted, publishResponse{
		OK:        true,
		Topic:     body.Topic,
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil))
}
