package operationhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const OperationBatchesRoute = "/v1/tenants/:tenant/projects/:project/operation-batches"
const BatchBodyLimit = 32 << 10

func OperationBatchHandler(store operations.Store, logger plumelog.StructuredLogger, preview bool) http.Handler {
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
		if r.Method != http.MethodPost {
			receipt, err := store.GetBatch(ctx, token, scope, router.Param(r, "batch"))
			writeBatchResult(w, r, http.StatusOK, receipt, err, logger)
			return
		}
		var input operations.BatchInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: BatchBodyLimit}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if preview {
			value, err := store.PreviewBatch(ctx, token, scope, input)
			writeBatchResult(w, r, http.StatusOK, value, err, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeBatchResult(w, r, http.StatusAccepted, nil, operations.ErrInvalid, logger)
			return
		}
		receipt, err := store.SubmitBatch(ctx, token, scope, keys[0], input)
		if err == nil {
			w.Header().Set("Location", "/v1/tenants/"+url.PathEscape(scope.Tenant)+"/projects/"+url.PathEscape(scope.Project)+"/operation-batches/"+receipt.ID)
		}
		writeBatchResult(w, r, http.StatusAccepted, receipt, err, logger)
	})
}

func writeBatchResult(w http.ResponseWriter, r *http.Request, status int, result any, err error, logger plumelog.StructuredLogger) {
	if err == nil {
		var writeErr error
		if status == http.StatusAccepted {
			writeErr = contract.WriteResponse(w, r, http.StatusAccepted, result, nil)
		} else {
			writeErr = contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}
		if writeErr != nil {
			logger.Warn("operation batch response write failed", plumelog.Fields{})
		}
		return
	}
	if errors.Is(err, identity.ErrUnauthenticated) {
		transport.WriteAccountError(w, r, err, logger)
		return
	}
	kind, message := contract.TypeUnavailable, "operation batches are unavailable"
	switch {
	case errors.Is(err, operations.ErrInvalid):
		kind, message = contract.TypeBadRequest, "invalid operation batch"
	case errors.Is(err, identity.ErrForbidden):
		kind, message = contract.TypeForbidden, "project or resource access is unavailable"
	case errors.Is(err, operations.ErrVolumeStale):
		kind, message = contract.TypePreconditionFailed, "current volume ownership revision required"
	case errors.Is(err, identity.ErrAccountStale):
		kind, message = contract.TypePreconditionFailed, "current instance ownership revision required"
	case errors.Is(err, operations.ErrConflict):
		kind, message = contract.TypeConflict, "operation batch conflicts with current state"
	case errors.Is(err, operations.ErrQuota):
		kind, message = contract.TypeRateLimited, "active operation quota cannot fit the batch"
	}
	builder := contract.NewErrorBuilder().Type(kind).Message(message)
	var item operations.BatchItemError
	if kind != contract.TypeUnavailable && errors.As(err, &item) {
		builder.Details(map[string]any{"index": item.Index})
	}
	transport.WriteAccountError(w, r, builder.Build(), logger)
}
