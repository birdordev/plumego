package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const accountsRoute = "/v1/tenants/:tenant/projects/:project/cloud-accounts"
const accountGrantsRoute = accountsRoute + "/:account/grants"
const accountSchedulesRoute = accountsRoute + "/:account/inventory-schedules"
const accountScheduleRoute = accountSchedulesRoute + "/:schedule"
const accountInputLimit = 4096
const accountRolloutRoute = accountsRoute + "/:account/operation-rollout"
const accountRolloutInputLimit = 24 << 10

func inventorySchedulesHandler(store controller.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		account := router.Param(r, "account")
		var result any
		var err error
		switch r.Method {
		case http.MethodPost:
			var input controller.RegisterSchedule
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
				result, err = store.RegisterSchedule(ctx, token, scope, account, input)
			}
		case http.MethodPatch:
			result, err = readScheduleChange(ctx, store, token, scope, account, r)
		default:
			query, parseErr := accountPageQuery(r.URL.RawQuery)
			var after int64
			if parseErr == nil && query.After != "" {
				after, parseErr = controller.ParseScheduleID(query.After)
			}
			err = parseErr
			if err == nil {
				result, err = store.ListSchedules(ctx, token, scope, account, after, query.Limit)
			}
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if item, ok := result.(controller.Schedule); ok {
			w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
		}
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusCreated, result, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}
		if err != nil {
			logger.Warn("inventory schedule response write failed", plumelog.Fields{})
		}
	})
}

func readScheduleChange(ctx context.Context, store controller.Store, token string, scope identity.Scope, account string, r *http.Request) (controller.Schedule, error) {
	id, err := controller.ParseScheduleID(router.Param(r, "schedule"))
	if err != nil {
		return controller.Schedule{}, err
	}
	revision, ok := accountRevision(r)
	if !ok {
		return controller.Schedule{}, identity.ErrAccountStale
	}
	var input controller.ChangeSchedule
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err != nil {
		return controller.Schedule{}, err
	}
	return store.ChangeSchedule(ctx, token, scope, account, id, revision, input)
}

func accountInventoryHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger, grants bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := accountPageQuery(r.URL.RawQuery)
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		var page any
		if grants {
			page, err = admin.ListGrants(ctx, token, scope, router.Param(r, "account"), query)
		} else {
			page, err = admin.List(ctx, token, scope, query)
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, page, nil); err != nil {
			logger.Warn("account inventory response write failed", plumelog.Fields{})
		}
	})
}

func accountPageQuery(raw string) (identity.AccountPageQuery, error) {
	query := identity.AccountPageQuery{Limit: identity.DefaultAccountPageLimit}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return query, identity.ErrAccountInvalid
	}
	for key, values := range params {
		if len(values) != 1 || values[0] == "" {
			return query, identity.ErrAccountInvalid
		}
		switch key {
		case "after":
			query.After = values[0]
		case "limit":
			query.Limit, err = strconv.Atoi(values[0])
			if err != nil {
				return query, identity.ErrAccountInvalid
			}
		default:
			return query, identity.ErrAccountInvalid
		}
	}
	return query, nil
}

func accountRolloutHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		account := router.Param(r, "account")
		var policy operations.RolloutPolicy
		var err error
		if r.Method == http.MethodPut {
			policy, err = replaceRolloutFromRequest(ctx, store, token, scope, account, r)
		} else {
			policy, err = store.AccountRollout(ctx, token, scope, account)
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(policy.Revision, 10)))
		if err := contract.WriteResponse(w, r, http.StatusOK, policy, nil); err != nil {
			logger.Warn("account operation rollout response write failed", plumelog.Fields{})
		}
	})
}

func replaceRolloutFromRequest(ctx context.Context, store operations.Store, token string, scope identity.Scope, account string, r *http.Request) (operations.RolloutPolicy, error) {
	revision, ok := minimumAccountRevision(r, 0)
	if !ok {
		return operations.RolloutPolicy{}, identity.ErrAccountStale
	}
	var input struct {
		Rules []operations.RolloutRule `json:"rules"`
	}
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountRolloutInputLimit}); err != nil {
		return operations.RolloutPolicy{}, err
	}
	return store.ReplaceAccountRollout(ctx, token, scope, account, revision, input.Rules)
}

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
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
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
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
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
	return minimumAccountRevision(r, 1)
}

func minimumAccountRevision(r *http.Request, minimum int64) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 || !strings.HasPrefix(values[0], `"`) || !strings.HasSuffix(values[0], `"`) || len(values[0]) < 3 {
		return 0, false
	}
	revision, err := strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	return revision, err == nil && revision >= minimum && strconv.Quote(strconv.FormatInt(revision, 10)) == values[0]
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
