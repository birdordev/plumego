package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"
)

type assetCapacityEvidence struct {
	Tenant          string                          `json:"tenant"`
	Project         string                          `json:"project"`
	ManifestSHA256  string                          `json:"manifest_sha256"`
	Rate            int                             `json:"requests_per_second"`
	DurationSeconds int                             `json:"duration_seconds"`
	Workers         int                             `json:"workers"`
	QueueLimit      int                             `json:"queue_limit"`
	MaxP95MS        int                             `json:"maximum_p95_ms"`
	StartedAt       *time.Time                      `json:"started_at,omitempty"`
	FinishedAt      time.Time                       `json:"finished_at"`
	Cases           []assetCapacityOutcome          `json:"cases"`
	Inventory       *assetCapacityInventoryEvidence `json:"inventory,omitempty"`
	ThresholdMet    bool                            `json:"threshold_met"`
}

type assetCapacityOutcome struct {
	Name           string  `json:"name"`
	Scheduled      int     `json:"scheduled"`
	Succeeded      int     `json:"succeeded"`
	Failed         int     `json:"failed"`
	Dropped        int     `json:"dropped"`
	P95MS          float64 `json:"p95_ms,omitempty"`
	PreflightError string  `json:"preflight_error,omitempty"`
	FirstFailure   string  `json:"first_failure,omitempty"`
}

func runAssetCapacityClient(ctx context.Context, projectURL *url.URL, tenant, project, token string,
	input io.Reader, output io.Writer, transport http.RoundTripper,
) error {
	if !validProjectClientToken(token) {
		return errors.New("a valid CLOUD_MANAGER_API_TOKEN is required")
	}
	manifest, digest, err := readAssetCapacityManifest(input, tenant, project)
	if err != nil {
		return err
	}
	monitor, err := prepareCapacityInventory(manifest.Inventory, projectURL, tenant, project, token, transport)
	if err != nil {
		return err
	}
	maxP95 := time.Duration(manifest.MaxP95MS) * time.Millisecond
	arrivals := assetArrivalPlan(manifest.DurationSeconds, manifest.Rate, manifest.Workers, maxP95)
	report := assetCapacityEvidence{Tenant: tenant, Project: project, ManifestSHA256: digest, Rate: manifest.Rate,
		DurationSeconds: manifest.DurationSeconds, Workers: manifest.Workers, QueueLimit: arrivals.waiting,
		MaxP95MS: manifest.MaxP95MS, Cases: make([]assetCapacityOutcome, len(manifest.Cases))}
	if monitor != nil {
		report.Inventory = &monitor.evidence
	}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); transport == nil && ok {
		pool := defaultTransport.Clone()
		pool.MaxIdleConns = max(pool.MaxIdleConns, manifest.Workers)
		pool.MaxIdleConnsPerHost = max(pool.MaxIdleConnsPerHost, manifest.Workers)
		defer pool.CloseIdleConnections()
		transport = pool
	}
	client := projectAPIClient(transport)
	target := projectURL.JoinPath("assets")
	requests := make([]*http.Request, len(manifest.Cases))
	preflightPassed := true
	for i, scenario := range manifest.Cases {
		request, err := http.NewRequest(http.MethodGet, scenario.target(target).String(), nil)
		if err != nil {
			return errors.New("invalid asset capacity request")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/json")
		requests[i] = request
		report.Cases[i].Name = scenario.Name
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = measureAssetCapacity(probeCtx, client, request, scenario)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report.Cases[i].PreflightError = err.Error()
			preflightPassed = false
		}
	}
	if preflightPassed && monitor != nil {
		preflightPassed = monitor.capture(ctx, "before", nil)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if preflightPassed {
		start := time.Now()
		startedAt := start.UTC()
		report.StartedAt = &startedAt
		var results []assetArrivalResult
		err := monitor.measure(ctx, func() error {
			var err error
			results, err = driveAssetArrivals(ctx, start, arrivals, func(ctx context.Context, i int, at time.Time) error {
				requestCtx, cancel := context.WithDeadline(ctx, at.Add(10*time.Second))
				defer cancel()
				index := i % len(requests)
				return measureAssetCapacity(requestCtx, client, requests[index], manifest.Cases[index])
			})
			return err
		})
		if err != nil {
			return err
		}
		report.ThresholdMet = summarizeAssetCapacity(results, report.Cases, maxP95) && (monitor == nil || monitor.evidence.ThresholdMet)
	}
	report.FinishedAt = time.Now().UTC()
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return err
	}
	if !report.ThresholdMet {
		return errors.New("asset capacity workload did not meet the release threshold")
	}
	return nil
}

func measureAssetCapacity(ctx context.Context, client *http.Client, request *http.Request, scenario assetCapacityQueryCase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	response, err := client.Do(request.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("asset API request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("asset API returned HTTP %d", response.StatusCode)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("invalid asset API response type")
	}
	limited := io.LimitedReader{R: response.Body, N: (1 << 20) + 1}
	decoder := json.NewDecoder(&limited)
	var envelope struct {
		Data assetCapacityPage `json:"data"`
	}
	if err := decoder.Decode(&envelope); err != nil {
		return errors.New("invalid asset API page")
	}
	if _, err := decoder.Token(); err != io.EOF || limited.N == 0 {
		return errors.New("invalid asset API page")
	}
	return scenario.check(envelope.Data)
}

func summarizeAssetCapacity(results []assetArrivalResult, cases []assetCapacityOutcome, maxP95 time.Duration) bool {
	passed := true
	for offset := range cases {
		scenario := &cases[offset]
		latencies := make([]time.Duration, 0, len(results)/len(cases)+1)
		for i := offset; i < len(results); i += len(cases) {
			result := results[i]
			scenario.Scheduled++
			switch {
			case result.dropped:
				scenario.Dropped++
			case result.err != nil:
				scenario.Failed++
				if scenario.FirstFailure == "" {
					scenario.FirstFailure = result.err.Error()
				}
			default:
				scenario.Succeeded++
				latencies = append(latencies, result.latency)
			}
		}
		p95 := assetLatencyP95(latencies)
		scenario.P95MS = float64(p95) / float64(time.Millisecond)
		passed = scenario.Failed == 0 && scenario.Dropped == 0 && p95 > 0 && p95 <= maxP95 && passed
	}
	return passed
}
