package main

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

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

const projectOperationsRoute = "/v1/tenants/:tenant/projects/:project/operations"

func operationListHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(operationQuery, store.List, logger)
}

func operationBacklogHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(parseBacklogQuery, store.Backlog, logger)
}

func operationLatencyHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveOperationQuery(parseLatencyQuery, store.Latency, logger)
}

func serveOperationQuery[Filter, Result any](parse func(string) (Filter, error),
	read func(context.Context, string, identity.Scope, Filter) (Result, error), logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
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
	writeAccountError(w, r, err, logger)
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
