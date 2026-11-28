package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/internal/operations"
)

type OperationLatencyGateReport struct {
	Version        int                          `json:"version"`
	Tenant         string                       `json:"tenant"`
	Project        string                       `json:"project"`
	From           time.Time                    `json:"from"`
	Until          time.Time                    `json:"until"`
	ManifestSHA256 string                       `json:"manifest_sha256"`
	Cases          []operationLatencyGateResult `json:"cases"`
	ThresholdMet   bool                         `json:"threshold_met"`
}

type operationLatencyGateResult struct {
	Case         OperationLatencyGateCase  `json:"case"`
	AsOf         *time.Time                `json:"as_of,omitempty"`
	Observed     *operations.LatencyTotals `json:"observed,omitempty"`
	RequestError string                    `json:"request_error,omitempty"`
	Issues       []string                  `json:"issues"`
	Passed       bool                      `json:"passed"`
}

func runOperationLatencyGate(ctx context.Context, base *url.URL, tenant, project, token string,
	input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if !projecthttp.ValidToken(token) {
		return errors.New("a valid CLOUD_MANAGER_API_TOKEN is required")
	}
	manifest, digest, err := readOperationLatencyGateManifest(input, tenant, project)
	if err != nil {
		return err
	}
	base = base.JoinPath("v1", "tenants", url.PathEscape(tenant), "projects", url.PathEscape(project), "operation-latency")
	report := OperationLatencyGateReport{Version: manifest.Version, Tenant: tenant, Project: project,
		From: manifest.From, Until: manifest.Until, ManifestSHA256: digest,
		Cases: make([]operationLatencyGateResult, 0, len(manifest.Cases)), ThresholdMet: true}
	for _, cohort := range manifest.Cases {
		result := checkOperationLatencyCase(ctx, base, manifest, cohort, token, transport)
		if err := ctx.Err(); err != nil {
			return err
		}
		report.Cases = append(report.Cases, result)
		report.ThresholdMet = report.ThresholdMet && result.Passed
	}
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return err
	}
	if !report.ThresholdMet {
		return errors.New("operation latency acceptance thresholds were not met")
	}
	return nil
}

func checkOperationLatencyCase(ctx context.Context, base *url.URL, manifest OperationLatencyGateManifest,
	cohort OperationLatencyGateCase, token string, transport http.RoundTripper) operationLatencyGateResult {
	result := operationLatencyGateResult{Case: cohort}
	query := projecthttp.Request{Target: cohort.Target(base, manifest.From, manifest.Until), Method: http.MethodGet, Status: http.StatusOK}
	var response bytes.Buffer
	if err := projecthttp.Run(ctx, query, token, nil, &response, transport); err != nil {
		result.RequestError = err.Error()
		result.Issues = []string{"request_failed"}
		return result
	}
	var snapshot operations.LatencyReport
	if err := json.Unmarshal(response.Bytes(), &snapshot); err != nil || !matchesOperationLatencyCase(snapshot, manifest, cohort) {
		result.Issues = []string{"snapshot_invalid"}
		return result
	}
	observed := snapshot.AsOf.UTC()
	result.AsOf = &observed
	if observed.Before(manifest.Until) {
		result.Issues = []string{"window_open"}
		return result
	}
	result.Observed = &snapshot.Summary
	result.Issues = latencyGateIssues(cohort, snapshot.Summary)
	result.Passed = len(result.Issues) == 0
	return result
}

func matchesOperationLatencyCase(snapshot operations.LatencyReport, manifest OperationLatencyGateManifest, cohort OperationLatencyGateCase) bool {
	expected := operations.LatencyScope{Tenant: manifest.Tenant, Project: manifest.Project,
		Account: cohort.Account, Region: cohort.Region, Action: cohort.Action}
	if snapshot.Scope != expected || !snapshot.From.Equal(manifest.From) || !snapshot.Until.Equal(manifest.Until) ||
		snapshot.AsOf.IsZero() || snapshot.Providers == nil || !validLatencyTotals(snapshot.Summary) {
		return false
	}
	if snapshot.Summary.Accepted == 0 {
		return len(snapshot.Providers) == 0
	}
	if len(snapshot.Providers) != 1 || snapshot.Providers[0].Provider != cohort.Provider {
		return false
	}
	provider := snapshot.Providers[0].LatencyTotals
	return validLatencyTotals(provider) && equalLatencyTotals(snapshot.Summary, provider)
}

func validLatencyTotals(totals operations.LatencyTotals) bool {
	if totals.Accepted < 0 || totals.Accepted > operations.LatencyLimit {
		return false
	}
	for _, count := range []int64{totals.Enqueued, totals.Succeeded, totals.Failed, totals.NeedsReview, totals.Pending} {
		if count < 0 || count > totals.Accepted {
			return false
		}
	}
	return totals.Accepted == totals.Succeeded+totals.Failed+totals.NeedsReview+totals.Pending &&
		validLatencyDistribution(totals.QueueWait, totals.Enqueued) && validLatencyDistribution(totals.Convergence, totals.Succeeded)
}

func validLatencyDistribution(distribution operations.LatencyDistribution, population int64) bool {
	if distribution.Samples < 0 || distribution.Samples > population {
		return false
	}
	if distribution.Samples == 0 {
		return distribution.P50MS == nil && distribution.P95MS == nil && distribution.MaxMS == nil
	}
	return distribution.P50MS != nil && distribution.P95MS != nil && distribution.MaxMS != nil &&
		*distribution.P50MS >= 0 && *distribution.P50MS <= *distribution.P95MS && *distribution.P95MS <= *distribution.MaxMS
}

func equalLatencyTotals(a, b operations.LatencyTotals) bool {
	return a.Accepted == b.Accepted && a.Enqueued == b.Enqueued && a.Succeeded == b.Succeeded && a.Failed == b.Failed &&
		a.NeedsReview == b.NeedsReview && a.Pending == b.Pending && equalLatencyDistribution(a.QueueWait, b.QueueWait) &&
		equalLatencyDistribution(a.Convergence, b.Convergence)
}

func equalLatencyDistribution(a, b operations.LatencyDistribution) bool {
	return a.Samples == b.Samples && (a.Samples == 0 || *a.P50MS == *b.P50MS && *a.P95MS == *b.P95MS && *a.MaxMS == *b.MaxMS)
}

func latencyGateIssues(cohort OperationLatencyGateCase, totals operations.LatencyTotals) []string {
	issues := []string{}
	if totals.Accepted < cohort.MinimumAccepted {
		issues = append(issues, "accepted_below_minimum")
	}
	if totals.Succeeded*percentBasis < totals.Accepted*cohort.MinimumSuccessBPS {
		issues = append(issues, "success_below_minimum")
	}
	if cohort.MaxPending != nil && totals.Pending > *cohort.MaxPending {
		issues = append(issues, "pending_above_maximum")
	}
	if cohort.MaxNeedsReview != nil && totals.NeedsReview > *cohort.MaxNeedsReview {
		issues = append(issues, "needs_review_above_maximum")
	}
	if totals.Enqueued == 0 || totals.QueueWait.Samples*percentBasis < totals.Enqueued*cohort.Queue.MinimumCoverageBPS {
		issues = append(issues, "queue_coverage_below_minimum")
	}
	if totals.Succeeded == 0 || totals.Convergence.Samples*percentBasis < totals.Succeeded*cohort.Convergence.MinimumCoverageBPS {
		issues = append(issues, "convergence_coverage_below_minimum")
	}
	if totals.QueueWait.P95MS != nil && *totals.QueueWait.P95MS > *cohort.Queue.MaximumP95MS {
		issues = append(issues, "queue_p95_above_maximum")
	}
	if totals.Convergence.P95MS != nil && *totals.Convergence.P95MS > *cohort.Convergence.MaximumP95MS {
		issues = append(issues, "convergence_p95_above_maximum")
	}
	return issues
}
