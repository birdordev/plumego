package operationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

func OperationEventsHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		after, limit, err := eventQuery(r.URL.RawQuery)
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		value, err := store.Events(ctx, token, scope, router.Param(r, "operation"), after, limit)
		if err != nil {
			writeOperationEventError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("operation events response write failed", plumelog.Fields{})
		}
	})
}

func writeOperationEventError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	if errors.Is(err, operations.ErrInvalid) {
		err = identity.ErrAccountInvalid
	}
	transport.WriteAccountError(w, r, err, logger)
}

func eventQuery(raw string) (int64, int, error) {
	if len(raw) > 1024 {
		return 0, 0, identity.ErrAccountInvalid
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, identity.ErrAccountInvalid
	}
	var after int64
	limit := 50
	for key, values := range query {
		if len(values) != 1 {
			return 0, 0, identity.ErrAccountInvalid
		}
		n, err := strconv.ParseUint(values[0], 10, 63)
		if err != nil {
			return 0, 0, identity.ErrAccountInvalid
		}
		switch key {
		case "after":
			after = int64(n)
		case "limit":
			if n < 1 || n > 100 {
				return 0, 0, identity.ErrAccountInvalid
			}
			limit = int(n)
		default:
			return 0, 0, identity.ErrAccountInvalid
		}
	}
	return after, limit, nil
}
