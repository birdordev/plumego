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
		value, err := store.PreviewDeletion(ctx, token, scope, operations.Input{Account: query.Get("account"), Region: query.Get("region"), AssetID: query.Get("asset_id")})
		if errors.Is(err, operations.ErrInvalid) {
			err = identity.ErrAccountInvalid
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("deletion preview response write failed", plumelog.Fields{})
		}
	})
}
