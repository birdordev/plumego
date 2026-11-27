package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const assetCapacityWorkload = `{"tenant":"bench","project":"ops","requests_per_second":20,"duration_seconds":1,
  "workers":4,"maximum_p95_ms":250,"cases":[
    {"name":"one","kind":"instance","limit":1,"account":"known","region":"zone","q":"稀有 +关键词","expected_items":1,
     "required_asset":{"account":"known","region":"zone","project":"ops","id":"instance-1"}},
    {"name":"none","kind":"instance","limit":50,"q":"absent","expected_items":0}]}`

const capacityOwned = `{"data":{"items":[{"kind":"instance","account":"known","region":"zone","project":"ops","id":"instance-1","name":"redacted-asset-name"}]}}`
const capacityEmpty = `{"data":{"items":[]}}`

func TestAssetCapacityClientTLS(t *testing.T) {
	var first, second atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/tenants/bench/projects/ops/assets" ||
			r.Header.Get("Accept") != "application/json" || r.Header.Get("Authorization") != "Bearer "+assetCapacityToken {
			t.Error("asset capacity client sent an unexpected request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query()
		switch {
		case query.Get("kind") == "instance" && query.Get("q") == "稀有 +关键词" && query.Get("account") == "known" &&
			query.Get("region") == "zone" && query.Get("limit") == "1" && len(query) == 5:
			first.Add(1)
			_, _ = io.WriteString(w, capacityOwned)
		case query.Get("kind") == "instance" && query.Get("q") == "absent" && query.Get("limit") == "50" && len(query) == 3:
			second.Add(1)
			_, _ = io.WriteString(w, capacityEmpty)
		default:
			t.Error("query escaped the workload contract")
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	report, output, err := runAssetCapacityFixture(t.Context(), server, assetCapacityWorkload)
	digest := sha256.Sum256([]byte(assetCapacityWorkload))
	if err != nil || !report.ThresholdMet || report.ManifestSHA256 != hex.EncodeToString(digest[:]) || report.StartedAt == nil ||
		report.FinishedAt.Sub(*report.StartedAt) < 950*time.Millisecond || report.QueueLimit != 1 || len(report.Cases) != 2 ||
		first.Load() != 11 || second.Load() != 11 {
		t.Fatalf("capacity workload did not run at the planned rate: %s %v", output, err)
	}
	for _, sample := range report.Cases {
		if sample.Scheduled != 10 || sample.Succeeded != 10 || sample.Failed != 0 || sample.Dropped != 0 || sample.P95MS <= 0 || sample.P95MS > 250 {
			t.Fatalf("wrong query observation: %+v", sample)
		}
	}
	for _, unmatched := range []string{assetCapacityToken, "稀有", "redacted-asset-name", "instance-1"} {
		if strings.Contains(output, unmatched) {
			t.Fatal("capacity report exposed credentials, query text, or response body")
		}
	}
}

func TestAssetCapacityClientRejectsPreflight(t *testing.T) {
	for _, test := range []struct {
		name, value, want string
		status            int
	}{
		{"off-scope", strings.Replace(capacityOwned, `"project":"ops"`, `"project":"private"`, 1), "outside the expected scope", 200},
		{"wrong-asset", strings.Replace(capacityOwned, "instance-1", "another-id", 1), "required item", 200},
		{"missing-items", `{"data":{}}`, "unexpected page size", 200},
		{"redirect", "unsafe response body", "HTTP 302", 302},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/redirected" {
					t.Error("request followed a redirect")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.value)
			}))
			t.Cleanup(server.Close)
			report, output, err := runAssetCapacityFixture(t.Context(), server, assetCapacityWorkload)
			if err == nil || report.ThresholdMet || report.StartedAt != nil || calls.Load() != 2 || len(report.Cases) != 2 ||
				!strings.Contains(report.Cases[0].PreflightError, test.want) || strings.Contains(output, "unsafe response body") {
				t.Fatalf("failed preflight produced load or unsafe evidence: %s %v", output, err)
			}
		})
	}
}

func TestAssetCapacityClientReportsOverload(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 2 {
			timer := time.NewTimer(60 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
			if calls.Load()%3 == 0 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("q") == "absent" {
			_, _ = io.WriteString(w, capacityEmpty)
		} else {
			_, _ = io.WriteString(w, capacityOwned)
		}
	}))
	t.Cleanup(server.Close)
	manifest := strings.NewReplacer(`"workers":4`, `"workers":1`, `"maximum_p95_ms":250`, `"maximum_p95_ms":10`, `"requests_per_second":20`, `"requests_per_second":100`).Replace(assetCapacityWorkload)
	report, output, err := runAssetCapacityFixture(t.Context(), server, manifest)
	if err == nil || report.ThresholdMet || report.StartedAt == nil || len(report.Cases) != 2 {
		t.Fatalf("overload accepted: %s %v", output, err)
	}
	dropped, failed := 0, 0
	for _, sample := range report.Cases {
		if sample.Scheduled != 50 || sample.Succeeded+sample.Dropped+sample.Failed != 50 {
			t.Fatalf("coordinated omission or unaccounted load: %+v", sample)
		}
		dropped += sample.Dropped
		failed += sample.Failed
	}
	if dropped == 0 || failed == 0 || int(calls.Load()) != 102-dropped {
		t.Fatalf("overload evidence is incomplete: %s", output)
	}
}

func TestAssetCapacityClientStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 3 {
			cancel()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("q") == "absent" {
			_, _ = io.WriteString(w, capacityEmpty)
		} else {
			_, _ = io.WriteString(w, capacityOwned)
		}
	}))
	t.Cleanup(server.Close)
	_, output, err := runAssetCapacityFixture(ctx, server, assetCapacityWorkload)
	if !errors.Is(err, context.Canceled) || output != "" || calls.Load() != 3 {
		t.Fatalf("canceled command continued scheduling or published incomplete evidence: %s %v", output, err)
	}
}

func TestAssetCapacityManifestRejectsInvalidCases(t *testing.T) {
	for _, manifest := range []string{
		strings.Replace(assetCapacityWorkload, `"project":"ops"`, `"project":"other"`, 1),
		strings.Replace(assetCapacityWorkload, `"name":"none"`, `"name":"one"`, 1),
		strings.Replace(assetCapacityWorkload, `"expected_items":0`, `"expected_items":null`, 1),
		strings.Replace(assetCapacityWorkload, `"expected_items":1`, `"expected_items":2`, 1),
		strings.Replace(assetCapacityWorkload, `"expected_items":0`, `"expected_items":0,"cursor":"opaque"`, 1),
		strings.Replace(assetCapacityWorkload, `"requests_per_second":20`, `"requests_per_second":1001`, 1),
		strings.Replace(assetCapacityWorkload, `"project":"ops","id"`, `"project":"outside","id"`, 1),
	} {
		if _, _, err := readAssetCapacityManifest(strings.NewReader(manifest), "bench", "ops"); err == nil {
			t.Fatal("unbounded, ambiguous or wrong-project manifest was accepted")
		}
	}
}

func runAssetCapacityFixture(ctx context.Context, server *httptest.Server, manifest string) (assetCapacityEvidence, string, error) {
	var output bytes.Buffer
	err := runGovernanceClient(ctx, []string{"asset-capacity", "bench", "ops"}, server.URL, assetCapacityToken,
		strings.NewReader(manifest), &output, server.Client().Transport)
	var report assetCapacityEvidence
	if readErr := json.Unmarshal(output.Bytes(), &report); err == nil && readErr != nil {
		err = readErr
	}
	return report, output.String(), err
}
