package governance

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/assets"
	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/identity"
)

const AuditRoute = "/v1/tenants/:tenant/projects/:project/audit"

func ProjectAuditHandler(store audit.Store, logger plumelog.StructuredLogger, export bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		q, err := ProjectAuditQuery(r.URL.RawQuery, export)
		if err != nil {
			writeAuditError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		if export {
			rows, err := store.Export(ctx, token, scope, q)
			if err != nil {
				writeAuditError(w, r, err, logger)
				return
			}
			if err := writeAuditCSV(w, rows); err != nil {
				logger.Warn("audit export response write failed", plumelog.Fields{})
			}
			return
		}
		page, err := store.List(ctx, token, scope, q)
		if err != nil {
			writeAuditError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, page, nil); err != nil {
			logger.Warn("audit list response write failed", plumelog.Fields{})
		}
	})
}

func ProjectAuditQuery(raw string, export bool) (audit.Query, error) {
	invalid := audit.ErrInvalidQuery
	if len(raw) > 4096 {
		return audit.Query{}, invalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return audit.Query{}, invalid
	}
	query := audit.Query{Limit: audit.DefaultLimit}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return audit.Query{}, invalid
		}
		value := entries[0]
		switch key {
		case "category":
			query.Category = value
		case "account":
			query.Account = value
		case "subject_id":
			query.Subject = value
		case "from", "until":
			stamp, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return audit.Query{}, invalid
			}
			if key == "from" {
				query.From = &stamp
			} else {
				query.Until = &stamp
			}
		case "cursor":
			if export {
				return audit.Query{}, invalid
			}
			query.Cursor = value
		case "limit":
			limit, err := strconv.Atoi(value)
			if export || err != nil || limit < 1 || limit > 200 {
				return audit.Query{}, invalid
			}
			query.Limit = limit
		default:
			return audit.Query{}, invalid
		}
	}
	return query, nil
}

func writeAuditError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, audit.ErrInvalidQuery):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid audit query").Build()
	case errors.Is(err, audit.ErrExportTooLarge):
		err = contract.NewErrorBuilder().Type(contract.TypeInvalidRequest).Message("audit export exceeds 2000 rows; narrow the filters").Build()
	case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, identity.ErrForbidden):
	default:
		err = contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("project audit is unavailable").Build()
	}
	transport.WriteAccountError(w, r, err, logger)
}

func writeAuditCSV(w http.ResponseWriter, rows []audit.Event) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="project-audit.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	out := csv.NewWriter(w)
	if err := out.Write([]string{"id", "occurred_at", "category", "action", "subject_id", "resource_kind", "account", "region", "actor", "related_project", "outcome", "revision"}); err != nil {
		return err
	}
	for _, row := range rows {
		revision := ""
		if row.Revision > 0 {
			revision = strconv.FormatInt(row.Revision, 10)
		}
		if err := out.Write([]string{row.ID, row.OccurredAt.UTC().Format(time.RFC3339Nano), row.Category, assets.SafeCSVCell(row.Action),
			assets.SafeCSVCell(row.SubjectID), assets.SafeCSVCell(row.ResourceKind), assets.SafeCSVCell(row.Account), assets.SafeCSVCell(row.Region),
			assets.SafeCSVCell(row.Actor), assets.SafeCSVCell(row.RelatedProject), assets.SafeCSVCell(row.Outcome), revision}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
