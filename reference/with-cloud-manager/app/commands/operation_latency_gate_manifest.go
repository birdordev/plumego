package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"time"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/operations"
)

const percentBasis = 10_000

type OperationLatencyGateManifest struct {
	Version int                        `json:"version"`
	Tenant  string                     `json:"tenant"`
	Project string                     `json:"project"`
	From    time.Time                  `json:"from"`
	Until   time.Time                  `json:"until"`
	Cases   []OperationLatencyGateCase `json:"cases"`
}

type OperationLatencyGateCase struct {
	Name              string                  `json:"name"`
	Provider          string                  `json:"provider"`
	Account           string                  `json:"account"`
	Region            string                  `json:"region"`
	Action            string                  `json:"action"`
	MinimumAccepted   int64                   `json:"minimum_accepted"`
	MinimumSuccessBPS int64                   `json:"minimum_success_bps"`
	MaxPending        *int64                  `json:"maximum_pending,omitempty"`
	MaxNeedsReview    *int64                  `json:"maximum_needs_review,omitempty"`
	Queue             OperationLatencyGateSLO `json:"queue"`
	Convergence       OperationLatencyGateSLO `json:"convergence"`
}

type OperationLatencyGateSLO struct {
	MinimumCoverageBPS int64  `json:"minimum_coverage_bps"`
	MaximumP95MS       *int64 `json:"maximum_p95_ms"`
}

func readOperationLatencyGateManifest(input io.Reader, tenant, project string) (OperationLatencyGateManifest, string, error) {
	var manifest OperationLatencyGateManifest
	data, err := projecthttp.ReadInput(input, 128<<10)
	if err != nil {
		return manifest, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	window := operations.LatencyFilter{}
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, "", errors.New("invalid operation latency acceptance manifest")
	}
	window.From, window.Until = manifest.From, manifest.Until
	if manifest.Version != 1 || manifest.Tenant != tenant || manifest.Project != project || !window.Valid() ||
		len(manifest.Cases) == 0 || len(manifest.Cases) > 32 {
		return manifest, "", errors.New("invalid operation latency acceptance manifest or project")
	}
	names := make(map[string]bool, len(manifest.Cases))
	scopes := make(map[[3]string]bool, len(manifest.Cases))
	for _, cohort := range manifest.Cases {
		key := [3]string{cohort.Account, cohort.Region, cohort.Action}
		if names[cohort.Name] || scopes[key] || !cohort.valid(window) {
			return manifest, "", errors.New("invalid or duplicate operation latency acceptance case")
		}
		names[cohort.Name], scopes[key] = true, true
	}
	manifest.From, manifest.Until = manifest.From.UTC(), manifest.Until.UTC()
	digest := sha256.Sum256(data)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func (cohort OperationLatencyGateCase) valid(filter operations.LatencyFilter) bool {
	filter.Account, filter.Region, filter.Action = cohort.Account, cohort.Region, cohort.Action
	return models.ValidRequestSegment(cohort.Name) && models.ValidRequestSegment(cohort.Provider) &&
		cohort.Account != "" && cohort.Region != "" && cohort.Action != "" && filter.Valid() &&
		cohort.MinimumAccepted >= 1 && cohort.MinimumAccepted <= operations.LatencyLimit && cohort.MinimumSuccessBPS >= 1 &&
		cohort.MinimumSuccessBPS <= percentBasis && validLatencyCountLimit(cohort.MaxPending) && validLatencyCountLimit(cohort.MaxNeedsReview) &&
		cohort.Queue.valid() && cohort.Convergence.valid()
}

func validLatencyCountLimit(limit *int64) bool {
	return limit == nil || *limit >= 0 && *limit <= operations.LatencyLimit
}

func (slo OperationLatencyGateSLO) valid() bool {
	return slo.MinimumCoverageBPS >= 1 && slo.MinimumCoverageBPS <= percentBasis && slo.MaximumP95MS != nil && *slo.MaximumP95MS >= 0
}

func (cohort OperationLatencyGateCase) Target(base *url.URL, from, until time.Time) *url.URL {
	target := *base
	target.RawQuery = url.Values{"from": {from.Format(time.RFC3339Nano)}, "until": {until.Format(time.RFC3339Nano)},
		"account": {cohort.Account}, "region": {cohort.Region}, "action": {cohort.Action}}.Encode()
	return &target
}
