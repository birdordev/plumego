package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

type healthResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
}

// WriteHealthResponse writes a standard health check response with status "ok".
func WriteHealthResponse(w http.ResponseWriter, r *http.Request, service string, logger plumelog.StructuredLogger) {
	if err := contract.WriteResponse(w, r, http.StatusOK, healthResponse{
		Status:    "ok",
		Service:   service,
		Timestamp: time.Now().UTC(),
	}, nil); err != nil && logger != nil {
		logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
	}
}
