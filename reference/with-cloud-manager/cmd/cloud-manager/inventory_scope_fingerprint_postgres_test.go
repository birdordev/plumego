package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const fixtureScopeManifest = `{"tenant":"lab","project":"compute","scopes":[
	{"provider":"tencent-cvm","account":"tc","region":"ap-test","kind":"instance"}
]}`

func TestAcceptanceInventoryScopeFingerprint(t *testing.T) {
	db, handler := prepareFreshnessReport(t)
	keys := []inventory.FreshnessScopeKey{
		{Provider: "huawei-ecs", Account: "hw", Region: "cn-test", Kind: "instance"},
		{Provider: "tencent-cvm", Account: "tc", Region: "ap-test", Kind: "instance"},
		{Provider: "alibaba-ecs", Account: "ali", Region: "cn-test", Kind: "instance"},
	}
	expected, err := inventory.FreshnessScopeFingerprint(keys)
	if err != nil {
		t.Fatal(err)
	}
	plain := getFreshnessReport(t, handler, freshnessPath)
	if plain.ScopeFingerprint != "" {
		t.Fatalf("fingerprint requires an explicit opt-in: %s", plain.ScopeFingerprint)
	}
	for _, query := range []string{"?scope_fingerprint=true", "?scope_fingerprint=true&state=fresh&dispatch_state=ready&limit=1"} {
		report := getFreshnessReport(t, handler, freshnessPath+query)
		if report.ScopeFingerprint != expected || report.Summary.Total != 3 {
			t.Fatalf("unexpected project fingerprint: %+v", report)
		}
	}
	for _, query := range []string{"?scope_fingerprint=1", "?scope_fingerprint=true&scope_fingerprint=false"} {
		requireFreshnessStatus(t, freshnessRequest(t, handler, freshnessPath+query, freshnessToken), http.StatusBadRequest)
	}
	expectedShared, err := inventory.FreshnessScopeFingerprint(keys[1:2])
	if err != nil {
		t.Fatal(err)
	}
	shared := getFreshnessReport(t, handler, strings.Replace(freshnessPath, "/compute/", "/shared/", 1)+"?scope_fingerprint=true")
	if shared.Summary.Total != 1 || shared.ScopeFingerprint != expectedShared {
		t.Fatalf("shared project included unauthorized scopes: %+v", shared)
	}

	store := inventory.NewStore(db)
	key := inventory.Key{Tenant: "lab", Account: "tc", Region: "ap-test", Kind: provider.Instance}
	finishFreshnessScan(t, store, startFreshnessScan(t, store, key))
	checkScopeManifestSample(t, handler, true)

	execFreshness(t, db, `UPDATE cmp_inventory_schedules SET region='ap-replacement'
		WHERE (tenant_id,account_id,region,kind)=('lab','tc','ap-test','instance')`)
	key.Region = "ap-replacement"
	finishFreshnessScan(t, store, startFreshnessScan(t, store, key))
	checkScopeManifestSample(t, handler, false)
}

func checkScopeManifestSample(t *testing.T, handler http.Handler, passed bool) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := runGovernanceClient(t.Context(), []string{"inventory-sample", "lab", "compute", "--provider", "tencent-cvm",
		"--scope-manifest", "--samples", "1"}, server.URL, freshnessToken, strings.NewReader(fixtureScopeManifest), &output, server.Client().Transport)
	if (err == nil) != passed {
		t.Fatalf("unexpected scope manifest result: pass=%t error=%v output=%s", passed, err, &output)
	}
	var sample inventorySample
	var summary inventorySampleResult
	reader := json.NewDecoder(&output)
	if reader.Decode(&sample) != nil || reader.Decode(&summary) != nil ||
		sample.Summary.Total != 1 || sample.Summary.Fresh != 1 ||
		!inventory.ValidFreshnessScopeFingerprint(sample.ScopeFingerprint) ||
		!inventory.ValidFreshnessScopeFingerprint(sample.ExpectedFingerprint) ||
		(sample.ScopeFingerprint == sample.ExpectedFingerprint) != passed ||
		sample.Passed != passed || summary.ThresholdMet != passed {
		t.Fatalf("unexpected scope manifest evidence: %+v / %+v", sample, summary)
	}
}
