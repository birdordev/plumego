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
const instanceResizeRoute = instancesRoute + "/resize"
const bucketEncryptionEnableRoute = "/v1/tenants/:tenant/projects/:project/object-buckets/encryption"
const bucketVersioningChangeRoute = "/v1/tenants/:tenant/projects/:project/object-buckets/versioning"
const imagesRoute = "/v1/tenants/:tenant/projects/:project/images"

const snapshotsRoute = "/v1/tenants/:tenant/projects/:project/snapshots"
const networkOperationsRoute = "/v1/tenants/:tenant/projects/:project/network-operations"
const dnsRecordCreationRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/records"
const dnsRecordDeletionRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-deletion"
const workloadScaleRoute = "/v1/tenants/:tenant/projects/:project/kubernetes-workloads/scale"
const dnsRecordUpdateRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-update"
const dnsRecordStatusRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-status"
const volumesRoute = "/v1/tenants/:tenant/projects/:project/volumes"
const volumeResizeRoute = volumesRoute + "/resize"
const volumeReleaseRoute = volumesRoute + "/release"
const volumeReleasePreviewRoute = volumesRoute + "/release-preview"
const databaseRenameRoute = "/v1/tenants/:tenant/projects/:project/databases/rename"
const databaseRecoveryRoute = "/v1/tenants/:tenant/projects/:project/databases/recover"
const bareMetalPXERoute = "/v1/tenants/:tenant/projects/:project/bare-metal/pxe"
const bareMetalPowerRoute = "/v1/tenants/:tenant/projects/:project/bare-metal/power"
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

func creationHandler[T any](submit func(context.Context, string, identity.Scope, string, T) (operations.Operation, error), logger plumelog.StructuredLogger) http.Handler {
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
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := submit(ctx, token, scope, keys[0], input)
		writeOperationReceipt(w, r, scope, op, err, logger)
	})
}

func bareMetalDeploymentHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		id := router.Param(r, "deployment")
		var d operations.BareMetalDeployment
		var err error
		status := http.StatusOK
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
			d, err = store.GetBareMetalDeployment(ctx, token, scope, id)
		case id == "":
			var input operations.BareMetalDeploymentInput
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err == nil {
				d, err = store.RegisterBareMetalDeployment(ctx, token, scope, input)
			}
			status = http.StatusCreated
		default:
			var input operations.BareMetalDeploymentReceipt
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err == nil {
				d, err = store.RecordBareMetalDeployment(ctx, token, scope, id, input)
			}
		}
		switch {
		case errors.Is(err, operations.ErrConflict):
			err = identity.ErrAccountConflict
		case errors.Is(err, operations.ErrInvalid):
			err = identity.ErrAccountInvalid
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if status == http.StatusCreated {
			w.Header().Set("Location", "/v1/tenants/"+url.PathEscape(scope.Tenant)+"/projects/"+url.PathEscape(scope.Project)+"/bare-metal/deployments/"+d.ID)
			err = contract.WriteResponse(w, r, http.StatusCreated, d, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, d, nil)
		}
		if err != nil {
			logger.Warn("bare-metal deployment response write failed", plumelog.Fields{})
		}
	})
}

func bareMetalReclaimHandler(store operations.Store, logger plumelog.StructuredLogger, create bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		if !create {
			result, err := store.GetBareMetalReclaim(ctx, token, scope, router.Param(r, "reclaim"))
			writeBareMetalReclaim(w, r, http.StatusOK, result, err, logger)
			return
		}
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
		var input operations.BareMetalReclaimInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		result, err := store.RequestBareMetalReclaim(ctx, token, scope, keys[0], revision, input)
		if err == nil {
			w.Header().Set("Location", "/v1/tenants/"+url.PathEscape(scope.Tenant)+"/projects/"+url.PathEscape(scope.Project)+"/bare-metal/reclaims/"+result.ID)
		}
		writeBareMetalReclaim(w, r, http.StatusAccepted, result, err, logger)
	})
}

func bareMetalCurrentReclaimHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 3 || len(query["account"]) != 1 || len(query["region"]) != 1 || len(query["system_id"]) != 1 {
			writeAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		input := operations.BareMetalReclaimTarget{Account: query.Get("account"), Site: query.Get("region"), SystemID: query.Get("system_id")}
		result, err := store.CurrentBareMetalReclaim(ctx, token, scope, input)
		if errors.Is(err, operations.ErrInvalid) {
			err = identity.ErrAccountInvalid
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, result, nil); err != nil {
			logger.Warn("current bare-metal reclaim response write failed", plumelog.Fields{})
		}
	})
}

func bareMetalReclaimTransitionHandler[T any](apply func(context.Context, string, identity.Scope, string, T) (operations.BareMetalReclaim, error),
	logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		result, err := apply(ctx, token, scope, router.Param(r, "reclaim"), input)
		writeBareMetalReclaim(w, r, http.StatusOK, result, err, logger)
	})
}

func writeBareMetalReclaim(w http.ResponseWriter, r *http.Request, status int, result operations.BareMetalReclaim, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, operations.ErrConflict):
		err = identity.ErrAccountConflict
	case errors.Is(err, operations.ErrInvalid):
		err = identity.ErrAccountInvalid
	}
	if err != nil {
		writeAccountError(w, r, err, logger)
		return
	}
	if status == http.StatusAccepted {
		err = contract.WriteResponse(w, r, http.StatusAccepted, result, nil)
	} else {
		err = contract.WriteResponse(w, r, http.StatusOK, result, nil)
	}
	if err != nil {
		logger.Warn("bare-metal reclaim response write failed", plumelog.Fields{})
	}
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
	case provider.Snapshot, provider.Volume, provider.Image, provider.LoadBalancer, provider.DNSZone, provider.KubernetesCluster, provider.DatabaseInstance, provider.CacheInstance, provider.BareMetal:
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

func bareMetalPowerRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current bare-metal ownership revision and inventory required").Build()
}

func cacheRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current cache name, ownership revision and inventory required").Build()
}

func bucketEncryptionRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current bucket revision and unconfigured encryption observation required").Build()
}

func bucketVersioningRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current bucket revision and versioning observation required").Build()
}

func instanceResizeRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current instance type, ownership revision and inventory required").Build()
}

func volumeRevisionAndCapacityError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current volume capacity, ownership revision and inventory required").Build()
}

func databaseRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current database name, ownership revision and inventory required").Build()
}

func databaseRecoveryRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current database ownership, recovery window and target placement required").Build()
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
