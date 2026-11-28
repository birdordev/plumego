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
	"with-cloud-manager/internal/provider"
)

const OperationsRoute = "/v1/tenants/:tenant/projects/:project/instance-operations"
const InstancesRoute = "/v1/tenants/:tenant/projects/:project/instances"
const InstanceResizeRoute = InstancesRoute + "/resize"
const BucketEncryptionEnableRoute = "/v1/tenants/:tenant/projects/:project/object-buckets/encryption"
const BucketVersioningChangeRoute = "/v1/tenants/:tenant/projects/:project/object-buckets/versioning"
const ImagesRoute = "/v1/tenants/:tenant/projects/:project/images"

const SnapshotsRoute = "/v1/tenants/:tenant/projects/:project/snapshots"
const NetworkOperationsRoute = "/v1/tenants/:tenant/projects/:project/network-operations"
const DNSRecordCreationRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/records"
const DNSRecordDeletionRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-deletion"
const WorkloadScaleRoute = "/v1/tenants/:tenant/projects/:project/kubernetes-workloads/scale"
const DNSRecordUpdateRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-update"
const DNSRecordStatusRoute = "/v1/tenants/:tenant/projects/:project/dns-zones/record-status"
const VolumesRoute = "/v1/tenants/:tenant/projects/:project/volumes"
const VolumeResizeRoute = VolumesRoute + "/resize"
const VolumeReleaseRoute = VolumesRoute + "/release"
const VolumeReleasePreviewRoute = VolumesRoute + "/release-preview"
const DatabaseRenameRoute = "/v1/tenants/:tenant/projects/:project/databases/rename"
const DatabaseRecoveryRoute = "/v1/tenants/:tenant/projects/:project/databases/recover"
const BareMetalPXERoute = "/v1/tenants/:tenant/projects/:project/bare-metal/pxe"
const BareMetalPowerRoute = "/v1/tenants/:tenant/projects/:project/bare-metal/power"
const CacheRenameRoute = "/v1/tenants/:tenant/projects/:project/caches/rename"
const LoadBalancerRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename"
const LoadBalancerListenerRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename-listener"
const LoadBalancerBackendAttachRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/attach-backend"
const LoadBalancerBackendDetachRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/detach-backend"
const LoadBalancerBackendWeightRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/backend-weight"
const LoadBalancerTargetGroupRenameRoute = "/v1/tenants/:tenant/projects/:project/load-balancers/rename-target-group"

func OperationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
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
		if r.Method == http.MethodPost {
			submitOperation(ctx, w, r, store, token, scope, logger)
			return
		}
		op, err := store.Get(ctx, token, scope, router.Param(r, "operation"))
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, op, nil); err != nil {
			logger.Warn("operation response write failed", plumelog.Fields{})
		}
	})
}

func submitOperation(ctx context.Context, w http.ResponseWriter, r *http.Request, store operations.Store, token string, scope identity.Scope, logger plumelog.StructuredLogger) {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		transport.WriteAccountError(w, r, identity.ErrAccountStale, logger)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
		return
	}
	var input operations.Input
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
		transport.WriteAccountError(w, r, err, logger)
		return
	}
	op, err := store.Submit(ctx, token, scope, keys[0], revision, input)
	WriteOperationReceipt(w, r, scope, op, err, logger)
}

func CreationHandler[T any](submit func(context.Context, string, identity.Scope, string, T) (operations.Operation, error), logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := submit(ctx, token, scope, keys[0], input)
		WriteOperationReceipt(w, r, scope, op, err, logger)
	})
}

func BareMetalDeploymentHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
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
			transport.WriteAccountError(w, r, err, logger)
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

func BareMetalReclaimHandler(store operations.Store, logger plumelog.StructuredLogger, create bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
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
		revision, ok := transport.AccountRevision(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrAccountStale, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		var input operations.BareMetalReclaimInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		result, err := store.RequestBareMetalReclaim(ctx, token, scope, keys[0], revision, input)
		if err == nil {
			w.Header().Set("Location", "/v1/tenants/"+url.PathEscape(scope.Tenant)+"/projects/"+url.PathEscape(scope.Project)+"/bare-metal/reclaims/"+result.ID)
		}
		writeBareMetalReclaim(w, r, http.StatusAccepted, result, err, logger)
	})
}

func BareMetalCurrentReclaimHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(query) != 3 || len(query["account"]) != 1 || len(query["region"]) != 1 || len(query["system_id"]) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
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
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, result, nil); err != nil {
			logger.Warn("current bare-metal reclaim response write failed", plumelog.Fields{})
		}
	})
}

func BareMetalReclaimTransitionHandler[T any](apply func(context.Context, string, identity.Scope, string, T) (operations.BareMetalReclaim, error),
	logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
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
		transport.WriteAccountError(w, r, err, logger)
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

func WriteOperationReceipt(w http.ResponseWriter, r *http.Request, scope identity.Scope, op operations.Operation, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, operations.ErrVolumeStale):
		err = VolumeRevisionError()
	case errors.Is(err, operations.ErrInvalid):
		err = identity.ErrAccountInvalid
	case errors.Is(err, operations.ErrConflict):
		err = identity.ErrAccountConflict
	case errors.Is(err, operations.ErrQuota):
		err = contract.NewErrorBuilder().Type(contract.TypeRateLimited).Message("instance or active operation quota exhausted").Build()
	}
	if err != nil {
		transport.WriteAccountError(w, r, err, logger)
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

func NetworkCreationHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		var input operations.NetworkInput
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		var parentRevision int64
		if input.Kind == provider.Subnet || input.Kind == provider.SecurityGroupRule || (input.Kind == provider.SecurityGroup && input.ParentID != "") {
			var ok bool
			parentRevision, ok = transport.AccountRevision(r)
			if !ok {
				transport.WriteAccountError(w, r, NetworkRevisionError(), logger)
				return
			}
		} else if len(r.Header.Values("If-Match")) != 0 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := store.SubmitNetworkCreation(ctx, token, scope, keys[0], parentRevision, input)
		if errors.Is(err, identity.ErrAccountStale) {
			err = NetworkRevisionError()
		}
		WriteOperationReceipt(w, r, scope, op, err, logger)
	})
}

func VersionedOperationHandler[T any](
	submit func(context.Context, string, identity.Scope, string, int64, T) (operations.Operation, error),
	revisionError func() error, logger plumelog.StructuredLogger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		revision, ok := transport.AccountRevision(r)
		if !ok {
			transport.WriteAccountError(w, r, revisionError(), logger)
			return
		}
		var input T
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		op, err := submit(ctx, token, scope, keys[0], revision, input)
		if errors.Is(err, identity.ErrAccountStale) {
			err = revisionError()
		}
		WriteOperationReceipt(w, r, scope, op, err, logger)
	})
}

func ElasticIPRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current elastic IP ownership revision required").Build()
}

func ElasticIPBindingRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current elastic IP and instance ownership revisions required").Build()
}

func NetworkDeletionRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current network resource ownership revision required").Build()
}

func NetworkRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current parent resource ownership revision required").Build()
}

func VolumeRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current volume ownership revision required").Build()
}

func ImageRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current image ownership revision required").Build()
}

func SourceInstanceRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current source instance ownership revision required").Build()
}

func LoadBalancerBackendAttachRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current instance and load balancer ownership revisions and exclusive target group required").Build()
}

func LoadBalancerBackendDetachRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("confirmed zero-weight member, exclusive target group and parent ownership revision required").Build()
}

func LoadBalancerBackendWeightRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current editable member weight, exclusive target group and parent ownership revision required").Build()
}

func LoadBalancerTargetGroupRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current target group name, parent ownership revision and exclusive association required").Build()
}

func LoadBalancerListenerRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current listener name, parent ownership revision and configuration snapshot required").Build()
}

func RetirementRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current Tencent instance ownership revision and observed system disk required").Build()
}

func BareMetalPowerRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current bare-metal ownership revision and inventory required").Build()
}

func CacheRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current cache name, ownership revision and inventory required").Build()
}

func BucketEncryptionRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current bucket revision and unconfigured encryption observation required").Build()
}

func BucketVersioningRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current bucket revision and versioning observation required").Build()
}

func InstanceResizeRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current instance type, ownership revision and inventory required").Build()
}

func VolumeRevisionAndCapacityError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current volume capacity, ownership revision and inventory required").Build()
}

func DatabaseRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current database name, ownership revision and inventory required").Build()
}

func DatabaseRecoveryRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current database ownership, recovery window and target placement required").Build()
}

func LoadBalancerRenameRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current load balancer name, ownership revision and inventory required").Build()
}

func DNSRecordRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current DNS zone ownership revision and record snapshot required").Build()
}

func WorkloadScaleRevisionError() error {
	return contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).
		Message("current cluster ownership revision and workload snapshot required").Build()
}
