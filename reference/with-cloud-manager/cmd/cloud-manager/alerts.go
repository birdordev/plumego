package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const alertsRoute = "/v1/tenants/:tenant/projects/:project/alerts"

func evaluateProjectAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	if err := billing.EvaluateProjectBudgetAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	if err := inventory.EvaluateBucketProtectionAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	if err := inventory.EvaluateTagPolicyAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	return alerts.EvaluateOperationQueueAlerts(ctx, tx, tenant, project)
}

func alertHandler(store alerts.Store, logger plumelog.StructuredLogger) http.Handler {
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
		id := router.Param(r, "alert")
		var item any
		var err error
		if id == "" {
			var query alerts.Query
			if query, err = alertQuery(r.URL.RawQuery); err == nil {
				item, err = store.List(ctx, token, scope, query)
			}
		} else {
			var alert alerts.Alert
			alert, err = lookupOrAcknowledgeAlert(ctx, r, store, token, scope, id)
			item = alert
			if err == nil {
				w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(alert.Revision, 10)))
			}
		}
		if err != nil {
			writeAlertError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, item, nil); err != nil {
			logger.Warn("alert response write failed", plumelog.Fields{})
		}
	})
}

func lookupOrAcknowledgeAlert(ctx context.Context, r *http.Request, store alerts.Store, token string, scope identity.Scope, id string) (alerts.Alert, error) {
	if r.Method != http.MethodPost {
		return store.Get(ctx, token, scope, id)
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return alerts.Alert{}, alerts.ErrInvalid
	}
	revision, ok := accountRevision(r)
	if !ok {
		return alerts.Alert{}, alerts.ErrStale
	}
	return store.Acknowledge(ctx, token, scope, id, revision)
}

func alertQuery(raw string) (alerts.Query, error) {
	q := alerts.Query{Limit: alerts.DefaultLimit}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, alerts.ErrInvalid
	}
	for name, list := range values {
		if len(list) != 1 {
			return q, alerts.ErrInvalid
		}
		switch name {
		case "status":
			q.Status = list[0]
		case "rule":
			q.Rule = list[0]
		case "after":
			q.After = list[0]
		case "limit":
			q.Limit, err = strconv.Atoi(list[0])
			if err != nil || q.Limit < 1 || q.Limit > 100 {
				return q, alerts.ErrInvalid
			}
		default:
			return q, alerts.ErrInvalid
		}
	}
	return q, nil
}

func writeAlertError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, alerts.ErrInvalid):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid alert query or parameters").Build()
	case errors.Is(err, alerts.ErrStale):
		err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current open alert ETag required").Build()
	}
	writeAccountError(w, r, err, logger)
}
