package assets

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func OwnershipHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		revision, ok := transport.AccountRevision(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrAccountStale, logger)
			return
		}
		var input inventory.OwnershipTransfer
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
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
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(value.Revision, 10)))
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("ownership response write failed", plumelog.Fields{})
		}
	})
}
