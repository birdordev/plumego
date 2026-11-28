package accounts

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const AccountsRoute = "/v1/tenants/:tenant/projects/:project/cloud-accounts"
const AccountGrantsRoute = AccountsRoute + "/:account/grants"
const AccountSchedulesRoute = AccountsRoute + "/:account/inventory-schedules"
const AccountScheduleRoute = AccountSchedulesRoute + "/:schedule"
const AccountInputLimit = 4096
const AccountRolloutRoute = AccountsRoute + "/:account/operation-rollout"
const AccountRolloutInputLimit = 24 << 10

func InventorySchedulesHandler(store controller.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
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
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: AccountInputLimit}); err == nil {
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
			transport.WriteAccountError(w, r, err, logger)
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
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return controller.Schedule{}, identity.ErrAccountStale
	}
	var input controller.ChangeSchedule
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: AccountInputLimit}); err != nil {
		return controller.Schedule{}, err
	}
	return store.ChangeSchedule(ctx, token, scope, account, id, revision, input)
}

func AccountInventoryHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger, grants bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := accountPageQuery(r.URL.RawQuery)
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
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
			transport.WriteAccountError(w, r, err, logger)
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

func AccountRolloutHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
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
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(policy.Revision, 10)))
		if err := contract.WriteResponse(w, r, http.StatusOK, policy, nil); err != nil {
			logger.Warn("account operation rollout response write failed", plumelog.Fields{})
		}
	})
}

func replaceRolloutFromRequest(ctx context.Context, store operations.Store, token string, scope identity.Scope, account string, r *http.Request) (operations.RolloutPolicy, error) {
	revision, ok := transport.MinimumAccountRevision(r, 0)
	if !ok {
		return operations.RolloutPolicy{}, identity.ErrAccountStale
	}
	var input struct {
		Rules []operations.RolloutRule `json:"rules"`
	}
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: AccountRolloutInputLimit}); err != nil {
		return operations.RolloutPolicy{}, err
	}
	return store.ReplaceAccountRollout(ctx, token, scope, account, revision, input.Rules)
}

func AccountHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
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
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: AccountInputLimit}); err == nil {
				account, err = admin.Register(ctx, token, scope, input)
			}
			status = http.StatusCreated
		case http.MethodPatch:
			revision, ok := transport.AccountRevision(r)
			if !ok {
				transport.WriteAccountError(w, r, identity.ErrAccountStale, logger)
				return
			}
			var input identity.AccountChange
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: AccountInputLimit}); err == nil {
				account, err = admin.Change(ctx, token, scope, id, revision, input)
			}
		case http.MethodGet, http.MethodHead:
			account, err = admin.Get(ctx, token, scope, id)
		}
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
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
