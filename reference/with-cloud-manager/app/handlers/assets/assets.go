package assets

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
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const AssetsRoute = "/v1/tenants/:tenant/projects/:project/assets"
const LoadBalancerConfigurationRoute = AssetsRoute + "/load-balancers/:asset/configuration"
const DatabaseBackupsRoute = AssetsRoute + "/databases/:asset/backups"
const DatabaseRestoreWindowsRoute = AssetsRoute + "/databases/:asset/restore-windows"
const DNSRecordsRoute = AssetsRoute + "/dns-zones/:asset/records"
const BucketVersioningRoute = AssetsRoute + "/object-buckets/:asset/versioning"
const BucketEncryptionRoute = AssetsRoute + "/object-buckets/:asset/encryption"

func BucketSettingHandler[T any](read func(context.Context, string, inventory.BucketSettingQuery) (T, error), logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(func(r *http.Request) (inventory.BucketSettingQuery, error) {
		if len(r.URL.RawQuery) > 8192 {
			return inventory.BucketSettingQuery{}, inventory.ErrInvalidQuery
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["account"]) != 1 {
			return inventory.BucketSettingQuery{}, inventory.ErrInvalidQuery
		}
		return inventory.BucketSettingQuery{
			Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"),
			ID: router.Param(r, "asset"), Account: values.Get("account"),
		}, nil
	}, read, func(w http.ResponseWriter, r *http.Request, snapshot T) error {
		return contract.WriteResponse(w, r, http.StatusOK, snapshot, nil)
	}, logger)
}

func AssetHandler[T any](queryInventory func(context.Context, string, inventory.Query) (T, error), logger plumelog.StructuredLogger) http.Handler {
	return InventoryHandler(queryInventory, func(w http.ResponseWriter, r *http.Request, assets T) error {
		return contract.WriteResponse(w, r, http.StatusOK, assets, nil)
	}, false, logger)
}

func InventoryHandler[T any](queryInventory func(context.Context, string, inventory.Query) (T, error),
	write func(http.ResponseWriter, *http.Request, T) error, export bool, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(func(r *http.Request) (inventory.Query, error) { return assetQuery(r, export) }, queryInventory, write, logger)
}

func DatabaseBackupsHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(databaseBackupsQuery, store.ReadDatabaseBackups,
		func(w http.ResponseWriter, r *http.Request, page inventory.DatabaseBackupPage) error {
			return contract.WriteResponse(w, r, http.StatusOK, page, nil)
		}, logger)
}

func DatabaseRestoreWindowsHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(databaseRestoreWindowsQuery, store.ReadDatabaseRestoreWindows,
		func(w http.ResponseWriter, r *http.Request, snapshot inventory.DatabaseRestoreWindows) error {
			return contract.WriteResponse(w, r, http.StatusOK, snapshot, nil)
		}, logger)
}

func LoadBalancerConfigurationHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(loadBalancerConfigurationQuery, store.ReadLoadBalancerConfiguration,
		func(w http.ResponseWriter, r *http.Request, result inventory.LoadBalancerConfiguration) error {
			return contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}, logger)
}

func ServeInventory[Q, T any](parse func(*http.Request) (Q, error), queryInventory func(context.Context, string, Q) (T, error),
	write func(http.ResponseWriter, *http.Request, T) error, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		reply := func(kind contract.ErrorType, message string) {
			if err := contract.WriteError(w, r, contract.NewErrorBuilder().Type(kind).Message(message).Build()); err != nil {
				logger.Warn("asset error response write failed", plumelog.Fields{})
			}
		}
		token, authenticated := transport.RequestCredential(r)
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-manager"`)
			reply(contract.TypeUnauthorized, "authentication required")
			return
		}
		query, err := parse(r)
		if err != nil {
			reply(contract.TypeBadRequest, "invalid asset query")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		assets, err := queryInventory(ctx, token, query)
		switch {
		case err == nil:
			if err := write(w, r, assets); err != nil {
				logger.Warn("asset response write failed", plumelog.Fields{})
			}
		case errors.Is(err, identity.ErrUnauthenticated):
			w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-manager"`)
			reply(contract.TypeUnauthorized, "authentication required")
		case errors.Is(err, identity.ErrForbidden):
			reply(contract.TypeForbidden, "project access is unavailable")
		case errors.Is(err, inventory.ErrLoadBalancerConfigurationNotFound):
			reply(contract.TypeNotFound, "load balancer configuration is unavailable")
		case errors.Is(err, inventory.ErrClusterNodePoolsChanged):
			reply(contract.TypeConflict, "cluster node pool snapshot changed")
		case errors.Is(err, inventory.ErrClusterNodePoolsNotFound):
			reply(contract.TypeNotFound, "cluster node pools are unavailable")
		case errors.Is(err, inventory.ErrClusterWorkloadsChanged):
			reply(contract.TypeConflict, "cluster workload snapshot changed")
		case errors.Is(err, inventory.ErrClusterWorkloadsNotFound):
			reply(contract.TypeNotFound, "cluster workloads are unavailable")
		case errors.Is(err, inventory.ErrDatabaseBackupsChanged):
			reply(contract.TypeConflict, "database backup snapshot changed")
		case errors.Is(err, inventory.ErrDatabaseBackupsNotFound):
			reply(contract.TypeNotFound, "database backup snapshot is unavailable")
		case errors.Is(err, inventory.ErrDatabaseRestoreWindowsNotFound):
			reply(contract.TypeNotFound, "database restore window snapshot is unavailable")
		case errors.Is(err, inventory.ErrDNSRecordsNotFound):
			reply(contract.TypeNotFound, "DNS zone records are unavailable")
		case errors.Is(err, inventory.ErrBucketSettingNotFound):
			reply(contract.TypeNotFound, "object bucket setting snapshot is unavailable")
		case errors.Is(err, inventory.ErrTagPolicyNotFound):
			reply(contract.TypeNotFound, "tag policy is unavailable")
		case errors.Is(err, inventory.ErrExportTooLarge):
			reply(contract.TypeInvalidRequest, "asset export exceeds 2000 rows; apply filters to narrow the export")
		case errors.Is(err, inventory.ErrInvalidQuery):
			reply(contract.TypeBadRequest, "invalid asset query")
		default:
			reply(contract.TypeUnavailable, "asset inventory is unavailable")
		}
	})
}

func loadBalancerConfigurationQuery(r *http.Request) (inventory.LoadBalancerConfigurationQuery, error) {
	if len(r.URL.RawQuery) > 8192 {
		return inventory.LoadBalancerConfigurationQuery{}, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 2 || len(values["account"]) != 1 || len(values["region"]) != 1 {
		return inventory.LoadBalancerConfigurationQuery{}, inventory.ErrInvalidQuery
	}
	return inventory.LoadBalancerConfigurationQuery{
		Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), ID: router.Param(r, "asset"),
		Account: values.Get("account"), Region: values.Get("region"),
	}, nil
}

func assetQuery(r *http.Request, export bool) (inventory.Query, error) {
	if len(r.URL.RawQuery) > 8192 {
		return inventory.Query{}, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return inventory.Query{}, inventory.ErrInvalidQuery
	}
	query := inventory.Query{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), Limit: 50}
	for key, entries := range values {
		if len(entries) != 1 {
			return inventory.Query{}, inventory.ErrInvalidQuery
		}
		switch key {
		case "account":
			query.Account = entries[0]
		case "region":
			query.Region = entries[0]
		case "kind":
			query.Kind = provider.Kind(entries[0])
		case "q":
			query.Search = entries[0]
		case "cursor":
			if export {
				return inventory.Query{}, inventory.ErrInvalidQuery
			}
			query.Cursor = entries[0]
		case "limit":
			if export {
				return inventory.Query{}, inventory.ErrInvalidQuery
			}
			query.Limit, err = strconv.Atoi(entries[0])
			if err != nil {
				return inventory.Query{}, inventory.ErrInvalidQuery
			}
		default:
			return inventory.Query{}, inventory.ErrInvalidQuery
		}
	}
	return query, nil
}
