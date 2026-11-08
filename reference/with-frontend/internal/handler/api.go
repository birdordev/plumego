// Package handler contains the JSON API handlers for the with-frontend demo.
package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

// APIHandler serves a minimal JSON API alongside the static frontend.
type APIHandler struct {
	Logger plumelog.StructuredLogger
}

// Status reports that the API is healthy.
func (h APIHandler) Status(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "with-frontend",
	}, nil))
}
