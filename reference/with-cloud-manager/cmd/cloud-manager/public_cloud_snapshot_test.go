package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const (
	huaweiFirstSnapshot    = "71000000-0000-4000-8000-000000000001"
	huaweiCopiedSnapshot   = "71000000-0000-4000-8000-000000000002"
	huaweiStandardSnapshot = "71000000-0000-4000-8000-000000000003"
	alibabaSnapshotCursor  = "snapshot+/=resume"
)

type cloudSnapshotFixture struct {
	publicCloudResourceFixture
	first, continued            atomic.Int32
	foreign, changed, newSource atomic.Bool
	v5Malformed                 atomic.Bool
}

func (f *cloudSnapshotFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	size := 40
	if f.changed.Load() {
		size = 80
	}
	source := f.id(provider.Volume)
	v2Path := "/v2/" + f.project + "/cloudsnapshots/detail"
	v5Path := "/v5/" + f.project + "/snapshots/detail"
	allow := func(continuation bool) bool {
		if !continuation {
			f.first.Add(1)
			return true
		}
		if f.continued.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return false
		}
		return true
	}
	invalid := func() bool {
		t.Error("unexpected cloud snapshot query")
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	switch {
	case r.Header.Get("X-TC-Action") == "DescribeSnapshots":
		body, err := io.ReadAll(r.Body)
		var query struct {
			Offset, Limit     int
			Order, OrderField string
			Filters           []struct {
				Name   string
				Values []string
			}
		}
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) || r.Method != http.MethodPost ||
			r.Host != "cbs.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != f.region ||
			r.Header.Get("X-TC-Token") != f.session || r.Header.Get("X-TC-Version") != "2017-03-12" ||
			json.Unmarshal(body, &query) != nil || query.Limit != 100 || (query.Offset != 0 && query.Offset != 1) ||
			query.Order != "ASC" || query.OrderField != "CREATE_TIME" || len(query.Filters) != 1 ||
			query.Filters[0].Name != "snapshot-type" || !slices.Equal(query.Filters[0].Values, []string{"PRIVATE_SNAPSHOT"}) {
			return invalid()
		}
		if !allow(query.Offset == 1) {
			return true
		}
		row := map[string]any{"SnapshotId": "snap-primary", "SnapshotName": "fixture-snapshot", "SnapshotType": "PRIVATE_SNAPSHOT",
			"SnapshotState": "NORMAL", "DiskId": source, "DiskSize": size, "Placement": map[string]string{"Zone": f.id(provider.Zone)}}
		if query.Offset == 1 {
			row["SnapshotId"], row["SnapshotName"], row["CopyFromRemote"] = "snap-copy", "", true
			if f.foreign.Load() {
				row["SnapshotType"] = "SHARED_SNAPSHOT"
			}
		} else if f.newSource.Load() {
			row["DiskId"] = "disk-2"
		}
		writeCloudFixtureJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 2, "SnapshotSet": []any{row}}})
	case r.Header.Get("X-ACS-Action") == "DescribeSnapshots":
		q := r.URL.Query()
		cursor := q.Get("NextToken")
		if r.Method != http.MethodPost || !validFixtureAlibabaSignature(r, f.key, f.secret) ||
			r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Security-Token") != f.session ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || q.Get("RegionId") != f.region ||
			q.Get("MaxResults") != "100" || (cursor != "" && cursor != alibabaSnapshotCursor) || q.Has("PageSize") || q.Has("PageNumber") {
			return invalid()
		}
		if !allow(cursor != "") {
			return true
		}
		row := map[string]any{"SnapshotId": "s-primary", "SnapshotName": "fixture-snapshot", "Status": "accomplished",
			"SourceDiskId": source, "SourceDiskSize": size, "RegionId": f.region, "Category": "standard"}
		next := alibabaSnapshotCursor
		if cursor != "" {
			next = ""
			row["SnapshotId"], row["SnapshotName"], row["SourceRegionId"] = "s-copy", "", "cn-beijing"
			if f.foreign.Load() {
				row["RegionId"] = "cn-beijing"
			}
		} else if f.newSource.Load() {
			row["SourceDiskId"] = "d-2"
		}
		writeCloudFixtureJSON(t, w, map[string]any{"NextToken": next, "Snapshots": map[string]any{"Snapshot": []any{row}}})
	case r.URL.Path == v2Path || r.URL.Path == v5Path:
		q := r.URL.Query()
		if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Method != http.MethodGet ||
			r.Header.Get("X-Auth-Token") != f.token || q.Get("limit") != "50" {
			return invalid()
		}
		row := map[string]any{"id": huaweiFirstSnapshot, "name": "fixture-snapshot", "volume_id": source, "size": size,
			"status": "available", "availability_zone": f.id(provider.Zone)}
		if r.URL.Path == v5Path {
			f.serveV5(t, w, q, row, invalid)
			return true
		}
		offset := q.Get("offset")
		if offset != "0" && offset != "1" {
			return invalid()
		}
		if !allow(offset == "1") {
			return true
		}
		row["os-extended-snapshot-attributes:project_id"] = f.project
		if offset == "1" {
			row["id"], row["name"], row["volume_id"] = huaweiCopiedSnapshot, "", ""
			if f.foreign.Load() {
				row["os-extended-snapshot-attributes:project_id"] = "00000000000000000000000000000000"
			}
		} else if f.newSource.Load() {
			row["volume_id"] = "73000000-0000-4000-8000-000000000002"
		}
		writeCloudFixtureJSON(t, w, map[string]any{"count": 2, "snapshots": []any{row}})
	default:
		return false
	}
	return true
}

func (f *cloudSnapshotFixture) serveV5(t *testing.T, w http.ResponseWriter, q url.Values, row map[string]any, invalid func() bool) {
	t.Helper()
	if q.Get("sort_key") != "id" || q.Get("sort_dir") != "asc" || q.Has("offset") {
		invalid()
		return
	}
	rows := []any{}
	switch q.Get("marker") {
	case "":
		row["category"] = "flash"
		rows = append(rows, row)
	case huaweiFirstSnapshot:
		if !f.v5Malformed.Load() {
			row["id"], row["category"], row["size"] = huaweiStandardSnapshot, "standard", "120"
		}
		rows = append(rows, row)
	case huaweiStandardSnapshot:
	default:
		invalid()
		return
	}
	writeCloudFixtureJSON(t, w, map[string]any{"snapshots": rows})
}

func (f *cloudSnapshotFixture) exercise(t *testing.T, db *sql.DB, app http.Handler, token, account, profile string,
	scan, scanV5 func(context.Context) error) {
	t.Helper()
	ctx := t.Context()
	source := f.id(provider.Volume)
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("snapshot continuation did not preserve throttling result: %v", err)
	}
	cursor := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT cursor FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='snapshot'", account).
			Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	checkpoint := cursor()
	if checkpoint == "" {
		t.Fatal("snapshot checkpoint empty after throttling")
	}
	f.foreign.Store(true)
	if err := scan(ctx); !provider.IsFailure(err, provider.Unauthorized) || cursor() != checkpoint {
		t.Fatalf("unexpected snapshot scope changed the checkpoint: %v", err)
	}
	f.foreign.Store(false)
	if err := scan(ctx); err != nil || f.first.Load() != 1 || f.continued.Load() != 3 || cursor() != "" {
		t.Fatalf("snapshot pagination first=%d continued=%d err=%v", f.first.Load(), f.continued.Load(), err)
	}
	q := "?kind=snapshot&account=" + url.QueryEscape(account)
	get := func(project, action, query string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets"+action+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		out := httptest.NewRecorder()
		app.ServeHTTP(out, req)
		return out
	}
	list := func(project, query string) inventory.Assets {
		t.Helper()
		var assets inventory.Assets
		decodeProjectData(t, get(project, "", query), &assets)
		return assets
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_inventory_objects o JOIN cmp_inventory_scopes s USING(scope_id)
		WHERE s.account_id=$1 AND s.kind='snapshot' AND o.removed_at IS NULL`, account).Scan(&count); err != nil || count != 2 {
		t.Fatalf("authorized pages did not persist: %d %v", count, err)
	}
	assets := list("project", q)
	if len(assets.Items) != 1 || assets.Items[0].ParentID != source || assets.Items[0].Snapshot == nil ||
		assets.Items[0].Snapshot.SizeGiB != 40 || assets.Items[0].OwnershipSource != "source_volume" {
		t.Fatalf("unresolved source leaked, or snapshot metadata is missing: %+v", assets)
	}
	if len(list("other", q).Items) != 0 {
		t.Fatal("account grant exposed another project's snapshots")
	}
	var topology inventory.Topology
	decodeProjectData(t, get("project", "/topology", q+"&q="+url.QueryEscape(source)), &topology)
	if len(topology.Nodes) != 1 || !slices.ContainsFunc(topology.Nodes[0].Links, func(link inventory.TopologyLink) bool {
		return link.Kind == provider.Volume && link.ID == source && link.Relation == "snapshot_of"
	}) {
		t.Fatalf("source volume topology missing: %+v", topology)
	}
	rows, err := csv.NewReader(get("project", "/export", q).Body).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[0]) != 12 || !slices.Equal(rows[0][9:], []string{"source_volume_id", "snapshot_class", "size_gib"}) ||
		!slices.Equal(rows[1][9:], []string{source, assets.Items[0].Snapshot.Class, "40"}) {
		t.Fatalf("snapshot export mismatch: %v %v", rows, err)
	}
	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: account, Region: f.region, Kind: provider.Snapshot}, f.provider, profile, scan)
	var changes inventory.Changes
	decodeProjectData(t, get("project", "/changes", q), &changes)
	if !slices.ContainsFunc(changes.Items, func(change inventory.Change) bool {
		return change.Action == "changed" && change.PreviousSnapshot != nil && change.Snapshot != nil &&
			change.PreviousSnapshot.SizeGiB == 40 && change.Snapshot.SizeGiB == 80 &&
			change.PreviousStatus != nil && *change.PreviousStatus == change.Status
	}) {
		t.Fatalf("snapshot capacity-only change missing: %+v", changes)
	}
	f.newSource.Store(true)
	if err := scan(ctx); !provider.IsFailure(err, provider.InvalidResponse) || cursor() != "" {
		t.Fatalf("a scan was allowed to replace an established source volume: %v", err)
	}
	f.newSource.Store(false)
	if scanV5 != nil {
		if err := scanV5(ctx); err != nil {
			t.Fatal(err)
		}
		page := list("project", q)
		if len(page.Items) != 2 || !slices.ContainsFunc(page.Items, func(asset inventory.Asset) bool {
			return asset.ID == huaweiStandardSnapshot && asset.Snapshot != nil && asset.Snapshot.Class == "standard" && asset.Snapshot.SizeGiB == 120
		}) {
			t.Fatalf("Huawei V5 did not include the standard snapshot after a short page: %+v", page)
		}
		var missingCandidate bool
		if err := db.QueryRowContext(ctx, `SELECT o.missing_generation IS NOT NULL AND o.removed_at IS NULL
			FROM cmp_inventory_objects o JOIN cmp_inventory_scopes s USING(scope_id)
			WHERE (s.account_id,s.kind,o.external_id)=($1,'snapshot',$2)`, account, huaweiCopiedSnapshot).
			Scan(&missingCandidate); err != nil || !missingCandidate {
			t.Fatalf("switching catalog coverage hid evidence of a missing snapshot: %t %v", missingCandidate, err)
		}
		f.v5Malformed.Store(true)
		if err := scanV5(ctx); !provider.IsFailure(err, provider.ExpiredCursor) || cursor() != "" {
			t.Fatalf("Huawei V5 accepted a non-advancing marker: %v", err)
		}
		f.v5Malformed.Store(false)
		if err := scanV5(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "UPDATE cmp_memberships SET role='admin' WHERE (tenant_id,project_id,principal_id)=('tenant','other','admin')"); err != nil {
		t.Fatal(err)
	}
	current := list("project", q)
	store := inventory.NewStore(db)
	ownership, err := store.TransferOwnership(ctx, token, identity.Scope{Tenant: "tenant", Project: "project"},
		current.Items[0].OwnershipRevision, inventory.OwnershipTransfer{Account: account, Region: f.region,
			Kind: provider.Volume, AssetID: source, TargetProject: "other"})
	if err != nil {
		t.Fatal(err)
	}
	moved := list("other", q)
	if len(list("project", q).Items) != 0 || len(moved.Items) != len(current.Items) || moved.Items[0].OwnershipRevision != ownership.Revision {
		t.Fatalf("snapshots did not follow the volume's project: %+v", moved)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_objects o SET removed_at=now()
		FROM cmp_inventory_scopes s WHERE s.scope_id=o.scope_id AND (s.account_id,s.kind,o.external_id)=($1,'volume',$2)`, account, source); err != nil {
		t.Fatal(err)
	}
	var remaining inventory.Topology
	decodeProjectData(t, get("other", "/topology", q), &remaining)
	if len(remaining.Nodes) != len(moved.Items) {
		t.Fatalf("deleting a source volume hid retained snapshots: %+v", remaining)
	}
	for _, node := range remaining.Nodes {
		if node.Asset.ParentID != source || slices.ContainsFunc(node.Links, func(link inventory.TopologyLink) bool {
			return link.Kind == provider.Volume
		}) {
			t.Fatalf("retained snapshot lost ownership or exposed a non-live topology link: %+v", node)
		}
	}
}
