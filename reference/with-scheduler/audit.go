package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type auditRequestKey struct{}

type auditIdentity struct {
	actor, requestID, reason string
}

func auditRequest(actor string, tracing *schedulerTracing, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestID := rand.Text()
		ctx := contract.WithRequestID(req.Context(), requestID)
		w.Header().Set(contract.RequestIDHeader, requestID)
		req = req.WithContext(ctx)
		reason := req.Header.Get("X-Change-Reason")
		switch reason {
		case "", "operator_request", "maintenance", "incident_recovery", "correction":
		default:
			writeError(w, req, contract.TypeBadRequest, "invalid change reason")
			return
		}
		ctx = context.WithValue(ctx, auditRequestKey{}, auditIdentity{actor, requestID, reason})
		ctx, span := tracing.tracer.StartTrace(ctx, "scheduler.command")
		span.Attributes["request_id"] = requestID
		defer tracing.finish(span)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r repository) beginAuditedTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	identity, ok := ctx.Value(auditRequestKey{}).(auditIdentity)
	if !ok {
		return tx, nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('scheduler.actor', $1, true),
		set_config('scheduler.request_id', $2, true), set_config('scheduler.reason_code', $3, true)`,
		identity.actor, identity.requestID, identity.reason); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
