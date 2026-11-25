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
	"with-cloud-manager/internal/provider"
)

const operationsRoute = "/v1/tenants/:tenant/projects/:project/instance-operations"
const instancesRoute = "/v1/tenants/:tenant/projects/:project/instances"
const networkOperationsRoute = "/v1/tenants/:tenant/projects/:project/network-operations"

func operationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
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
		if r.Method == http.MethodPost {
			submitOperation(ctx, w, r, store, token, scope, logger)
			return
		}
		op, err := store.Get(ctx, token, scope, router.Param(r, "operation"))
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, op, nil); err != nil {
			logger.Warn("operation response write failed", plumelog.Fields{})
		}
	})
}

func submitOperation(ctx context.Context, w http.ResponseWriter, r *http.Request, store operations.Store, token string, scope identity.Scope, logger plumelog.StructuredLogger) {
	revision, ok := accountRevision(r)
	if !ok {
		writeAccountError(w, r, identity.ErrAccountStale, logger)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeAccountError(w, r, identity.ErrAccountInvalid, logger)
		return
	}
	var input operations.Input
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
		writeAccountError(w, r, err, logger)
		return
	}
	op, err := store.Submit(ctx, token, scope, keys[0], revision, input)
	writeOperationReceipt(w, r, scope, op, err, logger)
}

func creationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		var input operations.CreateInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := store.SubmitCreation(ctx, token, scope, keys[0], input)
		writeOperationReceipt(w, r, scope, op, err, logger)
	})
}

func writeOperationReceipt(w http.ResponseWriter, r *http.Request, scope identity.Scope, op operations.Operation, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, operations.ErrVolumeStale):
		err = volumeRevisionError()
	case errors.Is(err, operations.ErrInvalid):
		err = identity.ErrAccountInvalid
	case errors.Is(err, operations.ErrConflict):
		err = identity.ErrAccountConflict
	case errors.Is(err, operations.ErrQuota):
		err = contract.NewErrorBuilder().Type(contract.TypeRateLimited).Message("instance or active operation quota exhausted").Build()
	}
	if err != nil {
		writeAccountError(w, r, err, logger)
		return
	}
	collection := "instance-operations"
	if op.ResourceKind == provider.Network || op.ResourceKind == provider.Subnet || op.ResourceKind == provider.SecurityGroup || op.ResourceKind == provider.SecurityGroupRule {
		collection = "network-operations"
	}
	w.Header().Set("Location", "/v1/tenants/"+url.PathEscape(scope.Tenant)+"/projects/"+url.PathEscape(scope.Project)+"/"+collection+"/"+op.ID)
	if err := contract.WriteResponse(w, r, http.StatusAccepted, op, nil); err != nil {
		logger.Warn("operation receipt write failed", plumelog.Fields{})
	}
}

func networkCreationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		var input operations.NetworkInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		var parentRevision int64
		if input.Kind == provider.Subnet || input.Kind == provider.SecurityGroupRule || (input.Kind == provider.SecurityGroup && input.ParentID != "") {
			var ok bool
			parentRevision, ok = accountRevision(r)
			if !ok {
				writeAccountError(w, r, networkRevisionError(), logger)
				return
			}
		} else if len(r.Header.Values("If-Match")) != 0 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := store.SubmitNetworkCreation(ctx, token, scope, keys[0], parentRevision, input)
		if errors.Is(err, identity.ErrAccountStale) {
			err = networkRevisionError()
		}
		writeOperationReceipt(w, r, scope, op, err, logger)
	})
}

func securityRuleRevocationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		revision, ok := accountRevision(r)
		if !ok {
			writeAccountError(w, r, networkRevisionError(), logger)
			return
		}
		var input operations.RuleRevocation
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := store.SubmitSecurityRuleRevocation(ctx, token, scope, keys[0], revision, input)
		if errors.Is(err, identity.ErrAccountStale) {
			err = networkRevisionError()
		}
		writeOperationReceipt(w, r, scope, op, err, logger)
	})
}

func networkRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current parent resource ownership revision required").Build()
}

func volumeRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current volume ownership revision required").Build()
}
