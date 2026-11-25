package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

func operationDecisionHandler[T any](decide func(context.Context, string, identity.Scope, string, T) (operations.Operation, error),
	logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := decide(ctx, token, scope, router.Param(r, "operation"), input)
		switch {
		case errors.Is(err, operations.ErrVolumeStale):
			err = volumeRevisionError()
		case errors.Is(err, operations.ErrInvalid):
			err = identity.ErrAccountInvalid
		case errors.Is(err, operations.ErrConflict):
			err = identity.ErrAccountConflict
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, op, nil); err != nil {
			logger.Warn("operation decision response write failed", plumelog.Fields{})
		}
	})
}
