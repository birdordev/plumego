package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
)

const accountsRoute = "/v1/tenants/:tenant/projects/:project/cloud-accounts"

func accountHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		id := router.Param(r, "account")
		var account identity.Account
		var err error
		status := http.StatusOK
		switch r.Method {
		case http.MethodPost:
			var input identity.AccountInput
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err == nil {
				account, err = admin.Register(ctx, token, scope, input)
			}
			status = http.StatusCreated
		case http.MethodPatch:
			revision, ok := accountRevision(r)
			if !ok {
				writeAccountError(w, r, identity.ErrAccountStale, logger)
				return
			}
			var input identity.AccountChange
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err == nil {
				account, err = admin.Change(ctx, token, scope, id, revision, input)
			}
		case http.MethodGet, http.MethodHead:
			account, err = admin.Get(ctx, token, scope, id)
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(account.Revision, 10)))
		if status == http.StatusCreated {
			w.Header().Set("Location", r.URL.EscapedPath()+"/"+account.ID)
			err = contract.WriteResponse(w, r, http.StatusCreated, account, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, account, nil)
		}
		if err != nil {
			logger.Warn("account response write failed", plumelog.Fields{})
		}
	})
}

func accountRevision(r *http.Request) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 || !strings.HasPrefix(values[0], `"`) || !strings.HasSuffix(values[0], `"`) || len(values[0]) < 3 {
		return 0, false
	}
	revision, err := strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	return revision, err == nil && revision > 0 && strconv.Quote(strconv.FormatInt(revision, 10)) == values[0]
}

func writeAccountError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	if apiErr, ok := contract.AsAPIError(err); ok {
		if err := contract.WriteError(w, r, apiErr); err != nil {
			logger.Warn("account error response write failed", plumelog.Fields{})
		}
		return
	}
	kind, message := contract.TypeUnavailable, "account administration is unavailable"
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-manager"`)
		kind, message = contract.TypeUnauthorized, "authentication required"
	case errors.Is(err, identity.ErrForbidden):
		kind, message = contract.TypeForbidden, "account or project access is unavailable"
	case errors.Is(err, identity.ErrAccountInvalid):
		kind, message = contract.TypeBadRequest, "invalid account action or parameters"
	case errors.Is(err, identity.ErrAccountStale):
		kind, message = contract.TypePreconditionFailed, "current account ETag required"
	case errors.Is(err, identity.ErrAccountConflict):
		kind, message = contract.TypeConflict, "account action conflicts with current state"
	}
	if err := contract.WriteError(w, r, contract.NewErrorBuilder().Type(kind).Message(message).Build()); err != nil {
		logger.Warn("account error response write failed", plumelog.Fields{})
	}
}
