package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"with-cloud-manager/internal/inventory"
)

const inventorySamplePercent = 95

type inventorySampleConfig struct {
	query       url.Values
	expected    int64
	manifest    bool
	fingerprint string
	samples     int
	interval    time.Duration
}

func inventorySampleOptions(args []string) (inventorySampleConfig, bool) {
	var options inventorySampleConfig
	flags := flag.NewFlagSet("inventory-sample", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Int64Var(&options.expected, "expected-scopes", 0, "")
	flags.BoolVar(&options.manifest, "scope-manifest", false, "")
	flags.IntVar(&options.samples, "samples", 31, "")
	flags.DurationVar(&options.interval, "interval", 30*time.Second, "")
	cloud := flags.String("provider", "", "")
	account := flags.String("account", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || options.expected < 0 || options.expected > 1_000_000_000 ||
		(options.expected == 0 && !options.manifest) ||
		options.samples < 1 || options.samples > 1440 || options.interval < time.Second || options.interval > time.Hour ||
		time.Duration(options.samples-1)*options.interval > 24*time.Hour {
		return inventorySampleConfig{}, false
	}
	options.query = url.Values{"limit": {"1"}, "window_seconds": {strconv.Itoa(inventory.DefaultFreshnessWindow)}}
	if *cloud != "" {
		options.query.Set("provider", *cloud)
	}
	if *account != "" {
		options.query.Set("account", *account)
	}
	return options, true
}

type inventorySampleScope struct {
	Tenant   string `json:"tenant"`
	Project  string `json:"project"`
	Provider string `json:"provider,omitempty"`
	Account  string `json:"account,omitempty"`
}

type inventorySample struct {
	Type string `json:"type"`
	inventorySampleScope
	Number              int                                `json:"number"`
	AsOf                time.Time                          `json:"as_of"`
	Window              int                                `json:"window_seconds"`
	ExpectedScopes      int64                              `json:"expected_scopes"`
	ExpectedFingerprint string                             `json:"expected_scope_fingerprint,omitempty"`
	ScopeFingerprint    string                             `json:"scope_fingerprint,omitempty"`
	Summary             inventory.FreshnessSummary         `json:"summary"`
	Dispatch            inventory.FreshnessDispatchSummary `json:"dispatch_summary"`
	Passed              bool                               `json:"passed"`
}

type inventorySampleResult struct {
	Type string `json:"type"`
	inventorySampleScope
	Samples      int  `json:"samples"`
	Passed       int  `json:"passed_samples"`
	MinPercent   int  `json:"minimum_fresh_percent"`
	ThresholdMet bool `json:"threshold_met"`
}

func runInventorySamples(ctx context.Context, plan operationRequestPlan, token string, output io.Writer, transport http.RoundTripper,
	options inventorySampleConfig, scope inventorySampleScope,
) error {
	encoder := json.NewEncoder(output)
	result := inventorySampleResult{Type: "summary", inventorySampleScope: scope, Samples: options.samples, MinPercent: inventorySamplePercent}
	var previous time.Time
	for i := range options.samples {
		if i != 0 {
			timer := time.NewTimer(options.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		var body bytes.Buffer
		if err := runProjectRequest(ctx, plan, token, nil, &body, transport); err != nil {
			return err
		}
		var snapshot struct {
			AsOf             time.Time                           `json:"as_of"`
			Window           int                                 `json:"window_seconds"`
			Summary          *inventory.FreshnessSummary         `json:"summary"`
			Dispatch         *inventory.FreshnessDispatchSummary `json:"dispatch_summary"`
			ScopeFingerprint string                              `json:"scope_fingerprint"`
		}
		if err := json.Unmarshal(body.Bytes(), &snapshot); err != nil || snapshot.AsOf.IsZero() ||
			!snapshot.AsOf.After(previous) || snapshot.Window != inventory.DefaultFreshnessWindow ||
			snapshot.Summary == nil || snapshot.Dispatch == nil || !validSampleCounts(*snapshot.Summary, *snapshot.Dispatch) {
			return errors.New("invalid or non-increasing inventory freshness snapshot")
		}
		previous = snapshot.AsOf
		if options.manifest && !inventory.ValidFreshnessScopeFingerprint(snapshot.ScopeFingerprint) {
			return errors.New("inventory freshness snapshot has no valid scope fingerprint")
		}
		freshEnough := snapshot.Summary.Fresh >= (options.expected*inventorySamplePercent+99)/100
		passed := snapshot.Summary.Total == options.expected && freshEnough &&
			(!options.manifest || snapshot.ScopeFingerprint == options.fingerprint)
		if passed {
			result.Passed++
		}
		if err := encoder.Encode(inventorySample{Type: "sample", inventorySampleScope: scope, Number: i + 1, AsOf: snapshot.AsOf,
			Window: snapshot.Window, ExpectedScopes: options.expected, ExpectedFingerprint: options.fingerprint,
			ScopeFingerprint: snapshot.ScopeFingerprint, Summary: *snapshot.Summary, Dispatch: *snapshot.Dispatch, Passed: passed}); err != nil {
			return err
		}
	}
	result.ThresholdMet = result.Passed == result.Samples
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if !result.ThresholdMet {
		return errors.New("inventory freshness samples did not meet the release threshold")
	}
	return nil
}

func validSampleCounts(summary inventory.FreshnessSummary, dispatch inventory.FreshnessDispatchSummary) bool {
	return matchesSampleTotal(summary.Total, summary.Fresh, summary.Stale, summary.Pending) &&
		matchesSampleTotal(summary.Total, dispatch.Scheduled, dispatch.Ready, dispatch.AccountBusy, dispatch.Cooldown, dispatch.Leased) &&
		dispatch.LongestReadyOverdue >= 0 && (dispatch.Ready != 0 || dispatch.LongestReadyOverdue == 0)
}

func matchesSampleTotal(total int64, counts ...int64) bool {
	for _, count := range counts {
		if count < 0 || count > total {
			return false
		}
		total -= count
	}
	return total == 0
}
