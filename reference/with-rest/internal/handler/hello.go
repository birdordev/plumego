package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

// HelloHandler serves the demo hello endpoint.
type HelloHandler struct {
	Logger plumelog.StructuredLogger
}

// ServeHTTP implements http.Handler.
func (h HelloHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, map[string]string{
		"message": "hello from with-rest",
	}, nil))
}
