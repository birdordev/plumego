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
	"with-cloud-manager/internal/provider"
)

const ProjectOperationsRoute = "/v1/tenants/:tenant/projects/:project/operations"
const OperationBudgetRoute = "/v1/tenants/:tenant/projects/:project/operation-budget"

func OperationBudgetHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		if r.URL.RawQuery != "" {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		var budget operations.ProjectBudget
		var err error
		if r.Method == http.MethodPut {
			revision, ok := transport.AccountRevision(r)
			if !ok {
				err = identity.ErrAccountStale
			} else {
				var input operations.ProjectBudgetInput
				err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 1024})
				if err == nil {
					budget, err = store.ReplaceProjectBudget(ctx, token, scope, revision, input)
				}
			}
		} else {
			budget, err = store.ReadProjectBudget(ctx, token, scope)
		}
		if errors.Is(err, identity.ErrAccountStale) {
			err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current project operation budget ETag required").Build()
		}
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(budget.Revision, 10)))
		if err := contract.WriteResponse(w, r, http.StatusOK, budget, nil); err != nil {
			logger.Warn("project operation budget response write failed", plumelog.Fields{})
		}
	})
}

func OperationListHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(operationQuery, store.List, logger)
}

func OperationBacklogHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(parseBacklogQuery, store.Backlog, logger)
}

func OperationLatencyHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(parseLatencyQuery, store.Latency, logger)
}

func serveOperationQuery[Filter, Result any](parse func(string) (Filter, error),
	read func(context.Context, string, identity.Scope, Filter) (Result, error), logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		q, err := parse(r.URL.RawQuery)
		if err != nil {
			writeOperationQueryError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		page, err := read(ctx, token, scope, q)
		if err != nil {
			writeOperationQueryError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, page, nil); err != nil {
			logger.Warn("operation query response write failed", plumelog.Fields{})
		}
	})
}

func writeOperationQueryError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, operations.ErrInvalidQuery):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid operation query").Build()
	case errors.Is(err, operations.ErrLatencyWindowTooLarge):
		err = contract.NewErrorBuilder().Type(contract.TypeInvalidRequest).Message("operation latency window contains too many operations").Build()
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrUnauthenticated):
	default:
		err = contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("operation history is unavailable").Build()
	}
	transport.WriteAccountError(w, r, err, logger)
}

func operationQuery(raw string) (operations.Query, error) {
	invalid := operations.ErrInvalidQuery
	if len(raw) > 4096 {
		return operations.Query{}, invalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return operations.Query{}, invalid
	}
	q := operations.Query{Limit: operations.DefaultOperationPageSize}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return operations.Query{}, invalid
		}
		value := entries[0]
		switch key {
		case "account":
			q.Account = value
		case "region":
			q.Region = value
		case "kind":
			q.Kind = provider.Kind(value)
		case "action":
			q.Action = value
		case "status":
			q.Status = value
		case "queue_issue":
			q.QueueIssue = value
		case "cursor":
			q.Cursor = value
		case "limit":
			limit, err := strconv.ParseUint(value, 10, 32)
			if err != nil || limit < 1 || limit > 100 {
				return operations.Query{}, invalid
			}
			q.Limit = int(limit)
		default:
			return operations.Query{}, invalid
		}
	}
	return q, nil
}
