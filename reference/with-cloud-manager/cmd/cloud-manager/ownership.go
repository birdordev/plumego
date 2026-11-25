package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func ownershipHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		revision, ok := accountRevision(r)
		if !ok {
			writeAccountError(w, r, identity.ErrAccountStale, logger)
			return
		}
		var input inventory.OwnershipTransfer
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		value, err := store.TransferOwnership(ctx, token, scope, revision, input)
		if err != nil {
			if errors.Is(err, inventory.ErrInvalidQuery) {
				err = identity.ErrAccountInvalid
			}
			writeAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(value.Revision, 10)))
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("ownership response write failed", plumelog.Fields{})
		}
	})
}
