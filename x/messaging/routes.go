package messaging

import (
	"net/http"
	"strings"

	"github.com/birdordev/plumego/router"
)

// RegisterRoutes binds the canonical messaging HTTP routes with explicit wiring.
func RegisterRoutes(r *router.Router, svc *Service, prefix string) error {
	if r == nil || svc == nil {
		return nil
	}
	if prefix == "" {
		prefix = "/api/v1/messages"
	}
	prefix = strings.TrimRight(prefix, "/")

	if err := r.AddRoute(http.MethodPost, prefix+"/send", http.HandlerFunc(svc.HandleSend)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodPost, prefix+"/batch", http.HandlerFunc(svc.HandleBatchSend)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, prefix+"/stats", http.HandlerFunc(svc.HandleStats)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, prefix+"/receipts", http.HandlerFunc(svc.HandleListReceipts)); err != nil {
		return err
	}
	if err := r.AddRoute(http.MethodGet, prefix+"/:id/receipt", http.HandlerFunc(svc.HandleGetReceipt)); err != nil {
		return err
	}
	return r.AddRoute(http.MethodGet, prefix+"/channels", http.HandlerFunc(svc.HandleChannelHealth))
}
