package cloudtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

const latencyGateFixtureToken = "fixture-latency-gate-only"

func TestOperationLatencyGateAcceptsScopedClosedWindows(t *testing.T) {
	manifest := latencyGateFixtureManifest()
	var requested atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/prefix/v1/tenants/lab/projects/compute/operation-latency" ||
			r.Header.Get("Authorization") != "Bearer "+latencyGateFixtureToken || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected latency acceptance request: %s %s", r.Method, r.URL)
		}
		for _, cohort := range manifest.Cases {
			if r.URL.Query().Get("account") == cohort.Account {
				base := *r.URL
				base.RawQuery = ""
				if r.URL.RawQuery != cohort.Target(&base, manifest.From.UTC(), manifest.Until.UTC()).RawQuery {
					t.Errorf("wrong acceptance query: %s", r.URL.RawQuery)
				}
				writeLatencyGateFixture(t, w, latencyGateFixtureReport(manifest, cohort))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	data := marshalLatencyGateManifest(t, manifest)
	var output bytes.Buffer
	err := commands.RunOperationsClient(t.Context(), []string{"verify-latency", "lab", "compute"}, server.URL+"/prefix", latencyGateFixtureToken,
		strings.NewReader("\n "+string(data)+"\n"), &output, server.Client().Transport)
	var report commands.OperationLatencyGateReport
	if err != nil || json.Unmarshal(output.Bytes(), &report) != nil || !report.ThresholdMet || len(report.Cases) != 3 ||
		requested.Load() != 3 || report.ManifestSHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("unexpected approval report: %s, %v", &output, err)
	}
	if report.Version != 1 || report.Tenant != "lab" || report.Project != "compute" || !report.From.Equal(manifest.From) || !report.Until.Equal(manifest.Until) {
		t.Fatalf("missing acceptance scope: %+v", report)
	}
	for i, result := range report.Cases {
		if !result.Passed || result.Observed == nil || result.Observed.Accepted != 10 || len(result.Issues) != 0 ||
			result.AsOf == nil || !result.AsOf.Equal(manifest.Until) || result.Case.Provider != manifest.Cases[i].Provider {
			t.Fatalf("threshold equality was not accepted: %+v", result)
		}
	}
}

func TestOperationLatencyGateArchivesFailuresAndRejectsMismatches(t *testing.T) {
	manifest := latencyGateFixtureManifest()
	manifest.Cases = nil
	scenarios := []struct {
		name   string
		change func(*operations.LatencyReport)
		issues []string
	}{
		{"thresholds", func(r *operations.LatencyReport) {
			r.Summary.Accepted, r.Summary.Enqueued = 9, 9
			r.Summary.Succeeded, r.Summary.Failed = 7, 2
			r.Summary.QueueWait = operations.LatencyDistribution{Samples: 5, P50MS: latencyGateNumber(99), P95MS: latencyGateNumber(101), MaxMS: latencyGateNumber(102)}
			r.Summary.Convergence = operations.LatencyDistribution{Samples: 5, P50MS: latencyGateNumber(99), P95MS: latencyGateNumber(501), MaxMS: latencyGateNumber(502)}
			r.Providers[0].LatencyTotals = r.Summary
		}, []string{"accepted_below_minimum", "success_below_minimum", "queue_coverage_below_minimum", "convergence_coverage_below_minimum", "queue_p95_above_maximum", "convergence_p95_above_maximum"}},
		{"unsettled", func(r *operations.LatencyReport) {
			r.Summary.Accepted, r.Summary.Enqueued, r.Summary.Succeeded = 20, 20, 18
			r.Summary.Failed, r.Summary.Pending, r.Summary.NeedsReview = 0, 1, 1
			r.Summary.QueueWait.Samples, r.Summary.Convergence.Samples = 18, 18
			r.Providers[0].LatencyTotals = r.Summary
		}, []string{"pending_above_maximum", "needs_review_above_maximum"}},
		{"wrong-provider", func(r *operations.LatencyReport) { r.Providers[0].Provider = provider.HuaweiECS }, []string{"snapshot_invalid"}},
		{"wrong-project", func(r *operations.LatencyReport) { r.Scope.Project = "other" }, []string{"snapshot_invalid"}},
		{"wrong-action", func(r *operations.LatencyReport) { r.Scope.Action = "stop" }, []string{"snapshot_invalid"}},
		{"wrong-window", func(r *operations.LatencyReport) { r.From = r.From.Add(time.Second) }, []string{"snapshot_invalid"}},
		{"open-window", func(r *operations.LatencyReport) { r.AsOf = r.Until.Add(-time.Microsecond) }, []string{"window_open"}},
		{"missing-p95", func(r *operations.LatencyReport) { r.Summary.QueueWait.P95MS = nil }, []string{"snapshot_invalid"}},
		{"invalid-population", func(r *operations.LatencyReport) { r.Summary.QueueWait.Samples = 11 }, []string{"snapshot_invalid"}},
		{"conflicting-counts", func(r *operations.LatencyReport) {
			r.Providers[0].QueueWait.Samples--
		}, []string{"snapshot_invalid"}},
		{"empty", func(r *operations.LatencyReport) {
			r.Summary, r.Providers = operations.LatencyTotals{}, []operations.ProviderLatency{}
		}, []string{"accepted_below_minimum", "queue_coverage_below_minimum", "convergence_coverage_below_minimum"}},
		{"forbidden", nil, []string{"request_failed"}},
		{"redirect", nil, []string{"request_failed"}},
		{"last-valid", func(*operations.LatencyReport) {}, []string{}},
	}
	for _, scenario := range scenarios {
		manifest.Cases = append(manifest.Cases, latencyGateFixtureCase(scenario.name, provider.TencentCVM))
	}
	var redirects atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirects.Add(1)
			return
		}
		for i, scenario := range scenarios {
			cohort := manifest.Cases[i]
			if r.URL.Query().Get("account") != cohort.Account {
				continue
			}
			switch scenario.name {
			case "forbidden":
				http.Error(w, "private fixture response", http.StatusForbidden)
			case "redirect":
				http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
			default:
				snapshot := latencyGateFixtureReport(manifest, cohort)
				scenario.change(&snapshot)
				writeLatencyGateFixture(t, w, snapshot)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	var output bytes.Buffer
	err := commands.RunOperationsClient(t.Context(), []string{"verify-latency", "lab", "compute"}, server.URL, latencyGateFixtureToken,
		bytes.NewReader(marshalLatencyGateManifest(t, manifest)), &output, server.Client().Transport)
	var report commands.OperationLatencyGateReport
	if err == nil || json.Unmarshal(output.Bytes(), &report) != nil || report.ThresholdMet || len(report.Cases) != len(scenarios) ||
		redirects.Load() != 0 || strings.Contains(output.String(), "private fixture response") || strings.Contains(output.String(), latencyGateFixtureToken) {
		t.Fatalf("unexpected failure report: %s, %v", &output, err)
	}
	for i, scenario := range scenarios {
		result := report.Cases[i]
		if result.Passed != (len(scenario.issues) == 0) || !slices.Equal(result.Issues, scenario.issues) || result.Case.Name != scenario.name {
			t.Fatalf("case %s reports %+v", scenario.name, result)
		}
		if slices.Contains(result.Issues, "snapshot_invalid") && result.Observed != nil {
			t.Fatalf("case %s archived untrusted results", scenario.name)
		}
	}
}

func TestOperationLatencyGateRejectsInvalidManifestsWithoutRequests(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid latency evidence initiated a network request")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	for _, mutate := range []func(*commands.OperationLatencyGateManifest){
		func(m *commands.OperationLatencyGateManifest) { m.Project = "other" },
		func(m *commands.OperationLatencyGateManifest) { m.Version = 2 },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[1].Name = m.Cases[0].Name },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[1].Account = m.Cases[0].Account },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[0].MinimumSuccessBPS = 0 },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[0].Queue.MaximumP95MS = nil },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[0].Convergence.MinimumCoverageBPS = 10001 },
		func(m *commands.OperationLatencyGateManifest) { m.Cases[0].MaxNeedsReview = latencyGateNumber(-1) },
		func(m *commands.OperationLatencyGateManifest) { m.From = m.From.Add(time.Nanosecond) },
	} {
		manifest := latencyGateFixtureManifest()
		mutate(&manifest)
		var output bytes.Buffer
		if err := commands.RunOperationsClient(t.Context(), []string{"verify-latency", "lab", "compute"}, server.URL, latencyGateFixtureToken,
			bytes.NewReader(marshalLatencyGateManifest(t, manifest)), &output, server.Client().Transport); err == nil || output.Len() != 0 {
			t.Fatalf("invalid manifest returned evidence: %s, %v", &output, err)
		}
	}
}

func latencyGateFixtureManifest() commands.OperationLatencyGateManifest {
	from := time.Date(2026, 11, 28, 8, 0, 0, 0, time.FixedZone("fixture", 8*60*60))
	return commands.OperationLatencyGateManifest{Version: 1, Tenant: "lab", Project: "compute", From: from, Until: from.Add(time.Minute),
		Cases: []commands.OperationLatencyGateCase{
			latencyGateFixtureCase("tencent-start", provider.TencentCVM),
			latencyGateFixtureCase("alibaba-start", provider.AlibabaECS),
			latencyGateFixtureCase("huawei-start", provider.HuaweiECS),
		}}
}

func latencyGateFixtureCase(name, cloud string) commands.OperationLatencyGateCase {
	return commands.OperationLatencyGateCase{Name: name, Provider: cloud, Account: name, Region: "region-a", Action: "start", MinimumAccepted: 10, MinimumSuccessBPS: 9000,
		MaxPending: latencyGateNumber(0), MaxNeedsReview: latencyGateNumber(0),
		Queue:       commands.OperationLatencyGateSLO{MinimumCoverageBPS: 9000, MaximumP95MS: latencyGateNumber(100)},
		Convergence: commands.OperationLatencyGateSLO{MinimumCoverageBPS: 10000, MaximumP95MS: latencyGateNumber(500)}}
}

func latencyGateNumber(value int64) *int64 { return &value }

func latencyGateFixtureReport(manifest commands.OperationLatencyGateManifest, cohort commands.OperationLatencyGateCase) operations.LatencyReport {
	totals := operations.LatencyTotals{Accepted: 10, Enqueued: 10, Succeeded: 9, Failed: 1,
		QueueWait:   operations.LatencyDistribution{Samples: 9, P50MS: latencyGateNumber(25), P95MS: latencyGateNumber(100), MaxMS: latencyGateNumber(150)},
		Convergence: operations.LatencyDistribution{Samples: 9, P50MS: latencyGateNumber(110), P95MS: latencyGateNumber(500), MaxMS: latencyGateNumber(750)}}
	return operations.LatencyReport{Scope: operations.LatencyScope{Tenant: manifest.Tenant, Project: manifest.Project,
		Account: cohort.Account, Region: cohort.Region, Action: cohort.Action}, From: manifest.From.UTC(), Until: manifest.Until.UTC(), AsOf: manifest.Until.UTC(),
		Summary: totals, Providers: []operations.ProviderLatency{{Provider: cohort.Provider, LatencyTotals: totals}}}
}

func writeLatencyGateFixture(t *testing.T, w http.ResponseWriter, report operations.LatencyReport) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Data operations.LatencyReport `json:"data"`
	}{report}); err != nil {
		t.Errorf("write test fixture: %v", err)
	}
}

func marshalLatencyGateManifest(t *testing.T, manifest commands.OperationLatencyGateManifest) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
