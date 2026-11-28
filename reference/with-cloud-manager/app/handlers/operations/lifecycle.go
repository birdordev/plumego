package operationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

func DeletionPreviewHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return AssetOperationPreviewHandler(func(ctx context.Context, token string, scope identity.Scope, account, region, id string) (operations.DeletionPreview, error) {
		return store.PreviewDeletion(ctx, token, scope, operations.Input{Account: account, Region: region, AssetID: id})
	}, logger)
}

func AssetOperationPreviewHandler[T any](read func(context.Context, string, identity.Scope, string, string, string) (T, error), logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		if len(r.URL.RawQuery) > 8192 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 3 || len(query["account"]) != 1 || len(query["region"]) != 1 || len(query["asset_id"]) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		value, err := read(ctx, token, scope, query.Get("account"), query.Get("region"), query.Get("asset_id"))
		switch {
		case errors.Is(err, operations.ErrInvalid):
			err = identity.ErrAccountInvalid
		case errors.Is(err, operations.ErrConflict):
			err = identity.ErrAccountConflict
		}
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusAccepted, value, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, value, nil)
		}
		if err != nil {
			logger.Warn("asset operation preview response write failed", plumelog.Fields{})
		}
	})
}

func OperationDecisionHandler[T any](decide func(context.Context, string, identity.Scope, string, T) (operations.Operation, error),
	logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := decide(ctx, token, scope, router.Param(r, "operation"), input)
		switch {
		case errors.Is(err, operations.ErrVolumeStale):
			err = VolumeRevisionError()
		case errors.Is(err, operations.ErrInvalid):
			err = identity.ErrAccountInvalid
		case errors.Is(err, operations.ErrConflict):
			err = identity.ErrAccountConflict
		case errors.Is(err, operations.ErrTicketRequired):
			err = contract.NewErrorBuilder().Type(contract.TypeConflict).Detail("reason", "change_ticket_required").
				Message("current external change approval required").Build()
		}
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, op, nil); err != nil {
			logger.Warn("operation decision response write failed", plumelog.Fields{})
		}
	})
}
