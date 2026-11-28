package cloudtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceAssetCapacityInventoryEvidence(t *testing.T) {
	db, handler := prepareFreshnessReport(t)
	store := inventory.NewStore(db)
	scopes := capacityInventoryFixtureScopes()
	for _, item := range scopes {
		key := inventory.Key{Tenant: "lab", Account: item.Account, Region: item.Region, Kind: provider.Kind(item.Kind)}
		finishFreshnessScan(t, store, startFreshnessScan(t, store, key))
	}
	execFreshness(t, db, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'test-instance','capacity-sentinel','running',generation
		FROM cmp_inventory_scopes WHERE (tenant_id,account_id,region,kind)=('lab','tc','ap-test','instance')`)
	zero, one := 0, 1
	manifest, err := json.Marshal(commands.AssetCapacityManifest{
		Tenant: "lab", Project: "compute", Workers: 4, Rate: 20, DurationSeconds: 2, MaxP95MS: 250,
		Cases: []commands.AssetCapacityQueryCase{
			{Name: "known", Kind: provider.Instance, Account: "tc", Limit: 3, ExpectedItems: &one,
				RequiredAsset: &commands.AssetCapacityIdentity{Project: "compute", Account: "tc", Region: "ap-test", ID: "test-instance"}},
			{Name: "absent", Kind: provider.Instance, Search: "none-in-this-project", Limit: 3, ExpectedItems: &zero},
		},
		Inventory: &commands.AssetCapacityInventoryManifest{IntervalSeconds: 1, Scopes: scopes},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, drift := range []bool{false, true} {
		var reads atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/tenants/lab/projects/compute/assets" && reads.Add(1) == 3 && drift {
				// 前两次资产请求属于预检。正式测量开始后替换计划范围，保留总数和有效完成记录。
				if _, err := db.ExecContext(r.Context(), `UPDATE cmp_inventory_schedules SET region='cn-replacement'
					WHERE (tenant_id,account_id,region,kind)=('lab','ali','cn-test','instance');
					UPDATE cmp_inventory_scopes SET region='cn-replacement'
					WHERE (tenant_id,account_id,region,kind)=('lab','ali','cn-test','instance')`); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
			}
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		var output bytes.Buffer
		err := commands.RunGovernanceClient(t.Context(), []string{"asset-capacity", "lab", "compute"}, server.URL, freshnessToken,
			bytes.NewReader(manifest), &output, server.Client().Transport)
		var report commands.AssetCapacityEvidence
		wantPassed := !drift
		if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil || (err != nil) != drift || report.Inventory == nil ||
			report.ThresholdMet != wantPassed || report.Inventory.ThresholdMet != wantPassed || len(report.Inventory.Rounds) < 4 || reads.Load() != 42 ||
			strings.Contains(output.String(), "fixture/private") || strings.Contains(output.String(), freshnessToken) {
			t.Fatalf("unexpected PostgreSQL qualification evidence: %s %v %v", output.String(), err, decodeErr)
		}
		for _, outcome := range report.Cases {
			if outcome.Scheduled != 20 || outcome.Succeeded != 20 || outcome.P95MS > 250 {
				t.Fatalf("inventory drift masked the query outcome: %+v", outcome)
			}
		}
		before := report.Inventory.Rounds[0]
		after := report.Inventory.Rounds[len(report.Inventory.Rounds)-1]
		if before.Phase != "before" || !before.Passed || after.Phase != "after" || len(after.Providers) != 3 {
			t.Fatalf("invalid qualification phases: %+v %+v", before, after)
		}
		for _, sample := range after.Providers {
			wantPassed := !drift || sample.Provider != provider.AlibabaECS
			if sample.Sample == nil || sample.Sample.Summary.Total != 1 || sample.Sample.Summary.Fresh != 1 ||
				sample.Sample.Passed != wantPassed || (sample.Sample.ScopeFingerprint == sample.Sample.ExpectedFingerprint) != wantPassed {
				t.Fatalf("equally fresh replacement scope went undetected or was counted incorrectly: %+v", sample.Sample)
			}
		}
	}
}
