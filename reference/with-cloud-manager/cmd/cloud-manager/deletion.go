package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

func deletionPreviewHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return assetOperationPreviewHandler(func(ctx context.Context, token string, scope identity.Scope, account, region, id string) (operations.DeletionPreview, error) {
		return store.PreviewDeletion(ctx, token, scope, operations.Input{Account: account, Region: region, AssetID: id})
	}, logger)
}

func assetOperationPreviewHandler[T any](read func(context.Context, string, identity.Scope, string, string, string) (T, error), logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		if len(r.URL.RawQuery) > 8192 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 3 || len(query["account"]) != 1 || len(query["region"]) != 1 || len(query["asset_id"]) != 1 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
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
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("asset operation preview response write failed", plumelog.Fields{})
		}
	})
}
