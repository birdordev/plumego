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
const imagesRoute = "/v1/tenants/:tenant/projects/:project/images"

const snapshotsRoute = "/v1/tenants/:tenant/projects/:project/snapshots"
const networkOperationsRoute = "/v1/tenants/:tenant/projects/:project/network-operations"
const dnsRecordCreationRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/records"
const dnsRecordDeletionRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-deletion"
const workloadScaleRoute = "/v1/tenants/:tenant/projects/:project/kubernetes-workloads/scale"
const dnsRecordStatusRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-status"
const databaseRenameRoute = "/v1/tenants/:tenant/projects/:project/databases/rename"
const cacheRenameRoute = "/v1/tenants/:tenant/projects/:project/caches/rename"
const loadBalancerRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename"
const loadBalancerListenerRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename-listener"
const loadBalancerBackendAttachRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/attach-backend"
const loadBalancerBackendDetachRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/detach-backend"
const loadBalancerBackendWeightRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/backend-weight"
const loadBalancerTargetGroupRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename-target-group"

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
	switch op.ResourceKind {
	case provider.Snapshot, provider.Volume, provider.Image, provider.LoadBalancer, provider.DNSZone, provider.KubernetesCluster, provider.DatabaseInstance, provider.CacheInstance:
		collection = "operations"
	case provider.Network, provider.Subnet, provider.SecurityGroup, provider.SecurityGroupRule, provider.ElasticIP:
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

func versionedOperationHandler[T any](
	submit func(context.Context, string, identity.Scope, string, int64, T) (operations.Operation, error),
	revisionError func() error, logger plumelog.StructuredLogger,
) http.Handler {
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
			writeAccountError(w, r, revisionError(), logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := submit(ctx, token, scope, keys[0], revision, input)
		if errors.Is(err, identity.ErrAccountStale) {
			err = revisionError()
		}
		writeOperationReceipt(w, r, scope, op, err, logger)
	})
}

func elasticIPRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current elastic IP ownership revision required").Build()
}

func elasticIPBindingRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current elastic IP and instance ownership revisions required").Build()
}

func networkDeletionRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current network resource ownership revision required").Build()
}

func networkRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current parent resource ownership revision required").Build()
}

func volumeRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current volume ownership revision required").Build()
}

func imageRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current image ownership revision required").Build()
}

func sourceInstanceRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current source instance ownership revision required").Build()
}

func loadBalancerBackendAttachRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current instance and load balancer ownership revisions and exclusive target group required").Build()
}

func loadBalancerBackendDetachRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("confirmed zero-weight member, exclusive target group and parent ownership revision required").Build()
}

func loadBalancerBackendWeightRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current editable member weight, exclusive target group and parent ownership revision required").Build()
}

func loadBalancerTargetGroupRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current target group name, parent ownership revision and exclusive association required").Build()
}

func loadBalancerListenerRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current listener name, parent ownership revision and configuration snapshot required").Build()
}

func retirementRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current Tencent instance ownership revision and observed system disk required").Build()
}

func cacheRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current cache name, ownership revision and inventory required").Build()
}

func databaseRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current database name, ownership revision and inventory required").Build()
}

func loadBalancerRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current load balancer name, ownership revision and inventory required").Build()
}

func dnsRecordRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current DNS zone ownership revision and record snapshot required").Build()
}

func workloadScaleRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current cluster ownership revision and workload snapshot required").Build()
}
