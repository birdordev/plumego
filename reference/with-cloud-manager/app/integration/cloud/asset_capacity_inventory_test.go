package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAssetCapacityInventoryEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		failSample int32
		mode       string
	}{
		{name: "full run"},
		{name: "wrong scope at baseline", failSample: 1, mode: "scope"},
		{name: "same size different scope during load", failSample: 2, mode: "scope"},
		{name: "one provider rejected final read", failSample: 3, mode: "denied"},
		{name: "one provider replayed its snapshot", failSample: 2, mode: "repeat"},
		{name: "insufficient fresh scopes", failSample: 2, mode: "stale"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scopes := capacityInventoryFixtureScopes()
			byProvider := make(map[string]inventory.FreshnessScopeKey, len(scopes))
			calls := make(map[string]*atomic.Int32, len(scopes))
			for _, key := range scopes {
				byProvider[key.Provider], calls[key.Provider] = key, &atomic.Int32{}
			}
			var assets atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+assetCapacityToken {
					t.Error("qualification used an unexpected method or authorization scope")
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/tenants/bench/projects/ops/assets":
					assets.Add(1)
					if r.URL.Query().Get("q") == "absent" {
						_, _ = io.WriteString(w, capacityEmpty)
					} else {
						_, _ = io.WriteString(w, capacityOwned)
					}
				case "/v1/tenants/bench/projects/ops/governance/inventory-freshness":
					query := r.URL.Query()
					key, ok := byProvider[query.Get("provider")]
					if !ok || query.Get("limit") != "1" || query.Get("window_seconds") != "900" || query.Get("scope_fingerprint") != "true" || len(query) != 4 {
						t.Error("qualification did not restrict the sample to the approved provider and scope fingerprint")
						w.WriteHeader(400)
						return
					}
					sequence := calls[key.Provider].Add(1)
					fresh, stale := int64(1), int64(0)
					if key.Provider == provider.AlibabaECS && sequence == test.failSample {
						switch test.mode {
						case "scope":
							key.Region = "replacement"
						case "denied":
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, "sensitive-response-body")
							return
						case "repeat":
							sequence--
						case "stale":
							fresh, stale = 0, 1
						}
					}
					printCapacityFreshness(t, w, key, sequence, fresh, stale)
				default:
					t.Error("qualification requested an unexpected path")
					w.WriteHeader(400)
				}
			}))
			t.Cleanup(server.Close)
			manifest := capacityInventoryFixtureManifest(t, scopes)
			report, output, err := runAssetCapacityFixture(t.Context(), server, manifest)
			wantPassed := test.failSample == 0
			if (err == nil) != wantPassed || report.ThresholdMet != wantPassed || report.Inventory == nil || report.Inventory.ThresholdMet != wantPassed ||
				strings.Contains(output, "sensitive-response-body") {
				t.Fatalf("unexpected qualification result: %s %v", output, err)
			}
			wantedRounds, wantedAssetReads := 3, int32(22)
			if test.failSample == 1 {
				wantedRounds, wantedAssetReads = 1, 2
			}
			if len(report.Inventory.Rounds) != wantedRounds || assets.Load() != wantedAssetReads ||
				(report.StartedAt == nil) != (test.failSample == 1) {
				t.Fatalf("qualification did not respect its preflight or phase boundaries: %s", output)
			}
			for i, round := range report.Inventory.Rounds {
				wantRoundPassed := int32(i+1) != test.failSample
				if round.Phase != []string{"before", "during", "after"}[i] || len(round.Providers) != 3 ||
					round.Passed != wantRoundPassed || round.FinishedAt.Before(round.StartedAt) {
					t.Fatalf("invalid round: %+v", round)
				}
			}
			if test.failSample != 0 {
				failed := report.Inventory.Rounds[test.failSample-1].Providers[0]
				if failed.Provider != provider.AlibabaECS || ((test.mode == "denied" || test.mode == "repeat") && failed.Failure == "") {
					t.Fatalf("wrong failed provider or missing failure evidence: %+v", failed)
				}
			}
		})
	}
}

func printCapacityFreshness(t *testing.T, w http.ResponseWriter, key inventory.FreshnessScopeKey, sequence int32, fresh, stale int64) {
	t.Helper()
	fingerprint, err := inventory.FreshnessScopeFingerprint([]inventory.FreshnessScopeKey{key})
	if err != nil {
		t.Error(err)
		w.WriteHeader(500)
		return
	}
	snapshot := inventory.FreshnessReport{AsOf: time.Unix(1800000000+int64(sequence), 0), Window: 900,
		Summary: inventory.FreshnessSummary{Total: 1, Fresh: fresh, Stale: stale}, DispatchSummary: inventory.FreshnessDispatchSummary{Scheduled: 1},
		ScopeFingerprint: fingerprint}
	if err := json.NewEncoder(w).Encode(map[string]any{"data": snapshot}); err != nil {
		t.Error(err)
	}
}

func capacityInventoryFixtureScopes() []inventory.FreshnessScopeKey {
	return []inventory.FreshnessScopeKey{
		{Provider: provider.TencentCVM, Account: "tc", Region: "ap-test", Kind: "instance"},
		{Provider: provider.AlibabaECS, Account: "ali", Region: "cn-test", Kind: "instance"},
		{Provider: provider.HuaweiECS, Account: "hw", Region: "cn-test", Kind: "instance"},
	}
}

func capacityInventoryFixtureManifest(t *testing.T, scopes []inventory.FreshnessScopeKey) string {
	t.Helper()
	m, _, err := commands.ReadAssetCapacityManifest(strings.NewReader(assetCapacityWorkload), "bench", "ops")
	if err != nil {
		t.Fatal(err)
	}
	m.Inventory = &commands.AssetCapacityInventoryManifest{Scopes: scopes}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAssetCapacityInventoryRejectsInvalidScopesBeforeNetwork(t *testing.T) {
	for _, scopes := range [][]inventory.FreshnessScopeKey{nil, append(capacityInventoryFixtureScopes(), capacityInventoryFixtureScopes()[0])} {
		manifest := capacityInventoryFixtureManifest(t, scopes)
		err := commands.RunGovernanceClient(t.Context(), []string{"asset-capacity", "bench", "ops"}, "https://fixture.example", assetCapacityToken,
			strings.NewReader(manifest), io.Discard, inventorySampleTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid inventory plan started network activity")
				return nil, nil
			}))
		if err == nil {
			t.Fatal("invalid inventory plan was accepted")
		}
	}
}
