package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"with-cloud-manager/internal/inventory"
)

const assetCapacityWindow = 30 * time.Second

func TestCapacityProjectAssetsAtTargetRate(t *testing.T) {
	requireAssetCapacity(t)
	server, _, db := startAssetCapacity(t)
	scopes := seedAssetCapacitySchedules(t, db)
	full, one, zero := 50, 1, 0
	workload := assetCapacityManifest{Tenant: "bench", Project: "ops", Rate: 200, DurationSeconds: 30, Workers: 16, MaxP95MS: 250,
		Cases: []assetCapacityQueryCase{
			{Name: "first", Kind: "instance", Limit: full, ExpectedItems: &full},
			{Name: "account", Kind: "instance", Account: "account-50", Limit: full, ExpectedItems: &full},
			{Name: "rare", Kind: "instance", Search: "needle", Limit: full, ExpectedItems: &one,
				RequiredAsset: &assetCapacityIdentity{Project: "ops", Account: "account-50", Region: "region-4", ID: "i-00500"}},
			{Name: "empty", Kind: "instance", Search: "no-such-asset", Limit: full, ExpectedItems: &zero},
		}, Inventory: &assetCapacityInventoryManifest{Scopes: scopes}}
	data, err := json.Marshal(workload)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runGovernanceClient(t.Context(), []string{"asset-capacity", "bench", "ops"}, server.URL, assetCapacityToken,
		bytes.NewReader(data), &output, nil)
	t.Log(output.String())
	if err != nil {
		t.Fatalf("target capacity CLI failed: %v", err)
	}
	var report assetCapacityEvidence
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || !report.ThresholdMet || len(report.Cases) != 4 ||
		report.Inventory == nil || !report.Inventory.ThresholdMet || len(report.Inventory.Rounds) < 3 {
		t.Fatalf("invalid capacity report: %v", err)
	}
	for _, sample := range report.Cases {
		if sample.Scheduled != 1500 || sample.Succeeded != 1500 {
			t.Fatalf("incomplete capacity workload: %+v", sample)
		}
	}
}

func seedAssetCapacitySchedules(t *testing.T, db *sql.DB) []inventory.FreshnessScopeKey {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `
		UPDATE cmp_inventory_scopes SET completed_credential_revision=credential_revision;
		INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,next_at,interval_seconds)
		SELECT tenant_id,account_id,region,kind,now()+interval '5 minutes',300 FROM cmp_inventory_scopes`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), `SELECT a.provider,s.account_id,s.region,s.kind
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		ORDER BY a.provider,s.account_id,s.region,s.kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var scopes []inventory.FreshnessScopeKey
	for rows.Next() {
		var key inventory.FreshnessScopeKey
		if err := rows.Scan(&key.Provider, &key.Account, &key.Region, &key.Kind); err != nil {
			t.Fatal(err)
		}
		scopes = append(scopes, key)
	}
	if err := rows.Err(); err != nil || len(scopes) != 200 {
		t.Fatalf("prepared %d inventory scopes: %v", len(scopes), err)
	}
	return scopes
}

func requireAssetCapacity(t *testing.T) {
	t.Helper()
	if os.Getenv("CLOUD_MANAGER_TEST_ASSET_TARGET_RATE") != "1" {
		t.Skip("CLOUD_MANAGER_TEST_ASSET_TARGET_RATE=1 enables the sustained HTTP capacity test")
	}
	if os.Getenv("CLOUD_MANAGER_TEST_DATABASE_URL") == "" {
		t.Fatal("CLOUD_MANAGER_TEST_DATABASE_URL is required for the sustained HTTP capacity test")
	}
}

func runAssetCapacityRate(t *testing.T, server *httptest.Server, cases []assetCapacityCase, start time.Time) {
	t.Helper()
	const rate = 200
	const maxP95 = 250 * time.Millisecond
	const workers = 16
	plan := assetArrivalPlan(int(assetCapacityWindow/time.Second), rate, workers, maxP95)
	results, err := driveAssetArrivals(t.Context(), start, plan, func(ctx context.Context, i int, _ time.Time) error {
		return readCapacityAssets(ctx, server.Client(), server.URL, cases[i%len(cases)])
	})
	if err != nil {
		t.Fatal(err)
	}
	latencies := make([]time.Duration, len(results))
	dropped, failures := 0, 0
	for i, result := range results {
		latencies[i] = result.latency
		if result.dropped {
			dropped++
		}
		if result.err != nil {
			if failures == 0 {
				t.Logf("first HTTP failure: %v", result.err)
			}
			failures++
		}
	}
	t.Logf("target=%d requests/s duration=%s succeeded=%d dropped=%d failed=%d elapsed=%s",
		rate, assetCapacityWindow, plan.requests-dropped-failures, dropped, failures, time.Since(start).Round(time.Millisecond))
	if dropped != 0 || failures != 0 {
		t.Errorf("expected %d dispatches and successful replies", plan.requests)
	}
	for offset, sample := range cases {
		p95 := assetCapacityP95(latencies, offset, len(cases))
		t.Logf("%s p95=%s", sample.name, p95.Round(time.Millisecond))
		if p95 == 0 || p95 > maxP95 {
			t.Errorf("%s: expected a positive p95 no higher than %s", sample.name, maxP95)
		}
	}
}
