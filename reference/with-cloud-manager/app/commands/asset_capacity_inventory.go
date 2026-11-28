package commands

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/internal/inventory"
)

type AssetCapacityInventoryManifest struct {
	IntervalSeconds int                           `json:"sample_interval_seconds"`
	Scopes          []inventory.FreshnessScopeKey `json:"scopes"`
}

type assetCapacityInventoryEvidence struct {
	MinimumFreshPercent int                           `json:"minimum_fresh_percent"`
	IntervalSeconds     int                           `json:"sample_interval_seconds"`
	Rounds              []assetCapacityInventoryRound `json:"rounds"`
	ThresholdMet        bool                          `json:"threshold_met"`
}

type assetCapacityInventoryRound struct {
	Phase      string                         `json:"phase"`
	StartedAt  time.Time                      `json:"started_at"`
	FinishedAt time.Time                      `json:"finished_at"`
	Providers  []assetCapacityInventorySample `json:"providers"`
	Passed     bool                           `json:"passed"`
}

type assetCapacityInventorySample struct {
	Provider string           `json:"provider"`
	Sample   *InventorySample `json:"sample,omitempty"`
	Failure  string           `json:"failure,omitempty"`
}

type assetCapacityInventoryProbe struct {
	scope   inventorySampleScope
	options inventorySampleConfig
	plan    projecthttp.Request
	asOf    time.Time
}

type assetCapacityInventoryMonitor struct {
	probes    []assetCapacityInventoryProbe
	token     string
	transport http.RoundTripper
	evidence  assetCapacityInventoryEvidence
}

func prepareCapacityInventory(manifest *AssetCapacityInventoryManifest, projectURL *url.URL, tenant, project, token string,
	transport http.RoundTripper,
) (*assetCapacityInventoryMonitor, error) {
	if manifest == nil {
		return nil, nil
	}
	interval := manifest.IntervalSeconds
	if interval == 0 {
		interval = 5
	}
	if interval < 1 || interval > 60 || len(manifest.Scopes) == 0 {
		return nil, errors.New("invalid asset capacity inventory schedule")
	}
	groups := make(map[string][]inventory.FreshnessScopeKey)
	for _, key := range manifest.Scopes {
		if key.Provider == "" || !validCapacityText(key.Provider, 128) {
			return nil, errors.New("invalid asset capacity inventory provider")
		}
		groups[key.Provider] = append(groups[key.Provider], key)
	}
	if len(groups) > 8 {
		return nil, errors.New("too many asset capacity inventory providers")
	}
	monitor := &assetCapacityInventoryMonitor{token: token, transport: transport,
		evidence: assetCapacityInventoryEvidence{MinimumFreshPercent: inventorySamplePercent, IntervalSeconds: interval,
			Rounds: make([]assetCapacityInventoryRound, 0, 3)}}
	for _, vendor := range slices.Sorted(maps.Keys(groups)) {
		scope := inventorySampleScope{Tenant: tenant, Project: project, Provider: vendor}
		options := inventorySampleConfig{manifest: true, query: url.Values{"provider": {vendor}, "limit": {"1"},
			"window_seconds": {strconv.Itoa(inventory.DefaultFreshnessWindow)}}}
		if err := options.setScopes(groups[vendor], scope); err != nil {
			return nil, err
		}
		target := projectURL.JoinPath("governance", "inventory-freshness")
		target.RawQuery = options.query.Encode()
		monitor.probes = append(monitor.probes, assetCapacityInventoryProbe{scope: scope, options: options,
			plan: projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}})
	}
	return monitor, nil
}

func (m *assetCapacityInventoryMonitor) capture(ctx context.Context, phase string, started chan<- struct{}) bool {
	round := assetCapacityInventoryRound{Phase: phase, StartedAt: time.Now().UTC(),
		Providers: make([]assetCapacityInventorySample, len(m.probes)), Passed: true}
	if started != nil {
		close(started)
	}
	number := len(m.evidence.Rounds) + 1
	var group sync.WaitGroup
	for i := range m.probes {
		group.Go(func() {
			probe := &m.probes[i]
			observation := assetCapacityInventorySample{Provider: probe.scope.Provider}
			sample, err := readInventorySample(ctx, probe.plan, m.token, m.transport, probe.options, probe.scope, probe.asOf, number)
			if err != nil {
				observation.Failure = err.Error()
			} else {
				probe.asOf = sample.AsOf
				observation.Sample = &sample
			}
			round.Providers[i] = observation
		})
	}
	group.Wait()
	for _, observation := range round.Providers {
		round.Passed = round.Passed && observation.Sample != nil && observation.Sample.Passed
	}
	round.FinishedAt = time.Now().UTC()
	m.evidence.Rounds = append(m.evidence.Rounds, round)
	return round.Passed
}

func (m *assetCapacityInventoryMonitor) measure(ctx context.Context, load func() error) error {
	if m == nil {
		return load()
	}
	stop, started, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		m.capture(ctx, "during", started)
		ticker := time.NewTicker(time.Duration(m.evidence.IntervalSeconds) * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			select {
			case <-stop:
				return
			default:
			}
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.capture(ctx, "during", nil)
			}
		}
	}()
	<-started
	err := load()
	completed := time.Now()
	close(stop)
	<-done
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	last := len(m.evidence.Rounds) - 1
	if m.evidence.Rounds[last].StartedAt.After(completed) {
		m.evidence.Rounds = m.evidence.Rounds[:last]
	}
	m.capture(ctx, "after", nil)
	m.evidence.ThresholdMet = len(m.evidence.Rounds) >= 3
	for _, round := range m.evidence.Rounds {
		m.evidence.ThresholdMet = m.evidence.ThresholdMet && round.Passed
	}
	return ctx.Err()
}
