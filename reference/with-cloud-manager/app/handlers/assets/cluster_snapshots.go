package assets

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/inventory"
)

const ClusterNodePoolsRoute = AssetsRoute + "/kubernetes-clusters/:asset/node-pools"

func ClusterNodePoolsHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(clusterSnapshotQuery, store.ReadClusterNodePools,
		func(w http.ResponseWriter, r *http.Request, result inventory.ClusterNodePoolsPage) error {
			return contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}, logger)
}

const ClusterWorkloadsRoute = AssetsRoute + "/kubernetes-clusters/:asset/workloads"

func ClusterWorkloadsHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(clusterSnapshotQuery, store.ReadClusterWorkloads,
		func(w http.ResponseWriter, r *http.Request, result inventory.ClusterWorkloadsPage) error {
			return contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}, logger)
}

func clusterSnapshotQuery(r *http.Request) (inventory.ClusterSnapshotQuery, error) {
	var query inventory.ClusterSnapshotQuery
	if len(r.URL.RawQuery) > 8192 {
		return query, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values["account"]) != 1 || len(values["region"]) != 1 {
		return query, inventory.ErrInvalidQuery
	}
	query = inventory.ClusterSnapshotQuery{
		Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), ID: router.Param(r, "asset"),
		Account: values.Get("account"), Region: values.Get("region"), Limit: 50,
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return query, inventory.ErrInvalidQuery
		}
		switch key {
		case "account", "region":
		case "snapshot":
			if entries[0] == "" {
				return query, inventory.ErrInvalidQuery
			}
			query.Snapshot = entries[0]
		case "limit":
			query.Limit, err = strconv.Atoi(entries[0])
		case "offset":
			query.Offset, err = strconv.Atoi(entries[0])
		default:
			return query, inventory.ErrInvalidQuery
		}
		if err != nil {
			return query, inventory.ErrInvalidQuery
		}
	}
	return query, nil
}
