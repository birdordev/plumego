package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
)

const assetCapacityToken = "fixture-capacity-project-asset-reader"

type assetCapacityCase struct {
	name, query string
	count       int
	wantAccount string
}

func BenchmarkProjectAssets(b *testing.B) {
	server, cases, _ := startAssetCapacity(b)
	latencies := make([]time.Duration, b.N)
	var index atomic.Int64
	b.SetParallelism(2)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := int(index.Add(1) - 1)
			sample := cases[i%len(cases)]
			start := time.Now()
			if err := readCapacityAssets(b.Context(), server.Client(), server.URL, sample); err != nil {
				b.Error(err)
			}
			latencies[i] = time.Since(start)
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "requests/s")
	for offset, sample := range cases {
		if p95 := assetCapacityP95(latencies, offset, len(cases)); p95 > 0 {
			b.ReportMetric(float64(p95)/float64(time.Millisecond), sample.name+"-p95-ms")
		}
	}
}

func assetCapacityP95(latencies []time.Duration, offset, stride int) time.Duration {
	group := make([]time.Duration, 0, len(latencies)/stride+1)
	for i := offset; i < len(latencies); i += stride {
		if latencies[i] > 0 {
			group = append(group, latencies[i])
		}
	}
	if len(group) == 0 {
		return 0
	}
	slices.Sort(group)
	return group[len(group)-len(group)/20-1]
}

func startAssetCapacity(t testing.TB) (*httptest.Server, []assetCapacityCase, *sql.DB) {
	t.Helper()
	db := dbtest.Open(t)
	seedAssetCapacity(t, db)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	server.Client().Timeout = 8 * time.Second
	cases := []assetCapacityCase{
		{name: "first", count: 50},
		{name: "account", query: "&account=account-50", count: 50, wantAccount: "account-50"},
		{name: "rare", query: "&q=needle", count: 1, wantAccount: "account-50"},
		{name: "empty", query: "&q=no-such-asset"},
	}
	for _, sample := range cases {
		if err := readCapacityAssets(t.Context(), server.Client(), server.URL, sample); err != nil {
			t.Fatal(err)
		}
	}
	return server, cases, db
}

func seedAssetCapacity(b testing.TB, db *sql.DB) {
	b.Helper()
	ctx := b.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		b.Fatal(err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('bench');
		INSERT INTO cmp_projects VALUES ('bench','ops');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
			VALUES ('bench','reader','service','capacity','reader');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('bench','ops','reader','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			SELECT 'bench','account-'||g,'ops',(ARRAY['tencent-cvm','alibaba-ecs','huawei-ecs'])[1+g%3],
				'remote-'||g,'fixture-profile','read_only' FROM generate_series(1,50) AS g;
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,running,lease_until,completed_at)
			SELECT tenant_id,account_id,'region-'||r,'instance',1,false,now(),now()
			FROM cmp_cloud_accounts CROSS JOIN generate_series(1,4) AS r;
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'i-'||lpad(g::text,5,'0'),CASE
				WHEN account_id='account-50' AND region='region-4' AND g=500 THEN 'end-of-fleet needle'
				ELSE 'instance '||g END,'running',1 FROM cmp_inventory_scopes CROSS JOIN generate_series(1,500) AS g;
		ANALYZE cmp_cloud_accounts;
		ANALYZE cmp_inventory_scopes;
		ANALYZE cmp_inventory_objects;`)
	if err != nil {
		b.Fatal(err)
	}
	digest := sha256.Sum256([]byte(assetCapacityToken))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'bench','reader',now()+interval '1 hour')`, digest[:]); err != nil {
		b.Fatal(err)
	}
}

func readCapacityAssets(ctx context.Context, client *http.Client, base string, sample assetCapacityCase) error {
	endpoint := base + "/v1/tenants/bench/projects/ops/assets?kind=instance&limit=50" + sample.query
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+assetCapacityToken)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %d", sample.name, response.StatusCode)
	}
	var payload struct{ Data inventory.Assets }
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Data.Items) != sample.count ||
		(sample.name == "rare" && payload.Data.Items[0].ID != "i-00500") {
		return fmt.Errorf("%s: unexpected result page with %d assets", sample.name, len(payload.Data.Items))
	}
	for _, item := range payload.Data.Items {
		if item.Account == "account-49" || (sample.wantAccount != "" && item.Account != sample.wantAccount) ||
			(item.Project != "ops" && item.Project != "shared") ||
			(sample.name == "rare" && (item.Project != "ops" || item.Region != "region-4" || item.Name != "end-of-fleet needle")) {
			return fmt.Errorf("%s: unexpected asset scope", sample.name)
		}
	}
	return nil
}
