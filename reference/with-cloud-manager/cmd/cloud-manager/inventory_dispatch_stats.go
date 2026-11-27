package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const dispatchStatsUsage = `usage: cloud-manager governance inventory-dispatch-stats --from TIME --until TIME [--expect TENANT/PROVIDER]... [--max-p95-ms MILLISECONDS] < controller.ndjson`

const maxDispatchStatsClaims = 250_000

type dispatchCohort struct {
	Tenant   string `json:"tenant"`
	Provider string `json:"provider"`
}

type dispatchStatsOptions struct {
	from, until time.Time
	expected    map[dispatchCohort]bool
	maxP95      int64
}

func parseDispatchStatsOptions(args []string) (dispatchStatsOptions, error) {
	options := dispatchStatsOptions{expected: make(map[dispatchCohort]bool)}
	flags := flag.NewFlagSet("inventory-dispatch-stats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	from := flags.String("from", "", "")
	until := flags.String("until", "", "")
	flags.Int64Var(&options.maxP95, "max-p95-ms", -1, "")
	flags.Func("expect", "", func(value string) error {
		tenant, cloud, found := strings.Cut(value, "/")
		cohort := dispatchCohort{Tenant: tenant, Provider: cloud}
		if !found || !validDispatchStatsLabel(tenant) || !validDispatchStatsLabel(cloud) || options.expected[cohort] {
			return errors.New("invalid or duplicate expected cohort")
		}
		options.expected[cohort] = true
		return nil
	})
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || options.maxP95 < -1 {
		return options, errors.New(dispatchStatsUsage)
	}
	start, startErr := time.Parse(time.RFC3339Nano, *from)
	end, endErr := time.Parse(time.RFC3339Nano, *until)
	if startErr != nil || endErr != nil || !start.Before(end) {
		return options, errors.New(dispatchStatsUsage)
	}
	options.from, options.until = start.UTC(), end.UTC()
	return options, nil
}

func validDispatchStatsLabel(value string) bool {
	return operationClientSegment(value) && strings.TrimSpace(value) == value
}

type dispatchClaimKey struct {
	ScheduleID int64 `json:"schedule_id"`
	Fence      int64 `json:"fence"`
}

type dispatchClaim struct {
	dispatchClaimKey
	dispatchCohort
	Account   string    `json:"account"`
	Kind      string    `json:"kind"`
	DueAt     time.Time `json:"due_at"`
	ClaimedAt time.Time `json:"claimed_at"`
}

type dispatchWaitStats struct {
	Claims int   `json:"claims"`
	P50    int64 `json:"p50_ms"`
	P95    int64 `json:"p95_ms"`
	Max    int64 `json:"max_ms"`
}

type dispatchCohortStats struct {
	dispatchCohort
	dispatchWaitStats
}

type dispatchStatsReport struct {
	From         time.Time                    `json:"from"`
	Until        time.Time                    `json:"until"`
	InputDigest  string                       `json:"input_sha256"`
	Claims       int                          `json:"claims"`
	Duplicates   int                          `json:"duplicates"`
	MaxP95       *int64                       `json:"max_p95_ms,omitempty"`
	Providers    map[string]dispatchWaitStats `json:"providers"`
	Tenants      map[string]dispatchWaitStats `json:"tenants"`
	Cohorts      []dispatchCohortStats        `json:"cohorts"`
	Missing      []dispatchCohort             `json:"missing_cohorts"`
	OverLimit    []dispatchCohort             `json:"over_limit_cohorts"`
	ThresholdMet bool                         `json:"threshold_met"`
}

func runDispatchStats(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	options, err := parseDispatchStatsOptions(args)
	if err != nil {
		return err
	}
	digest := sha256.New()
	claims, duplicates, err := readDispatchClaims(ctx, options, io.TeeReader(input, digest))
	if err != nil {
		return err
	}
	report := summarizeDispatchClaims(options, claims)
	report.Duplicates = duplicates
	report.InputDigest = hex.EncodeToString(digest.Sum(nil))
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return err
	}
	if !report.ThresholdMet {
		return errors.New("inventory dispatch evidence did not meet the requested threshold")
	}
	return nil
}

func readDispatchClaims(ctx context.Context, options dispatchStatsOptions, input io.Reader) (map[dispatchClaimKey]dispatchClaim, int, error) {
	scanner := bufio.NewScanner(input)
	claims := make(map[dispatchClaimKey]dispatchClaim)
	duplicates := 0
	for line := 1; scanner.Scan(); line++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		record, included, err := decodeDispatchClaim(scanner.Bytes(), options)
		if err != nil {
			return nil, 0, fmt.Errorf("inventory dispatch log line %d: %w", line, err)
		}
		if !included {
			continue
		}
		if prior, exists := claims[record.dispatchClaimKey]; exists {
			if prior != record {
				return nil, 0, fmt.Errorf("inventory dispatch log line %d: conflicting claim for the same schedule and fence", line)
			}
			duplicates++
			continue
		}
		if len(claims) >= maxDispatchStatsClaims {
			return nil, 0, errors.New("inventory dispatch claim limit exceeded; use a shorter window")
		}
		claims[record.dispatchClaimKey] = record
	}
	if scanner.Err() != nil {
		return nil, 0, errors.New("inventory dispatch log could not be read or exceeds the per-line size limit")
	}
	return claims, duplicates, ctx.Err()
}

func decodeDispatchClaim(line []byte, options dispatchStatsOptions) (dispatchClaim, bool, error) {
	var event *struct {
		Message string `json:"msg"`
	}
	if err := json.Unmarshal(line, &event); err != nil || event == nil {
		return dispatchClaim{}, false, errors.New("invalid log object")
	}
	if event.Message != "inventory_dispatch_claimed" {
		return dispatchClaim{}, false, nil
	}
	var record struct {
		dispatchClaim
		Schema int    `json:"schema_version"`
		Wait   *int64 `json:"due_wait_ms"`
	}
	if err := json.Unmarshal(line, &record); err != nil || record.Schema != 1 || record.ScheduleID < 1 || record.Fence < 1 ||
		!validDispatchStatsLabel(record.Tenant) || !validDispatchStatsLabel(record.Provider) ||
		!validDispatchStatsLabel(record.Account) || !validDispatchStatsLabel(record.Kind) ||
		record.DueAt.IsZero() || record.ClaimedAt.IsZero() || record.ClaimedAt.Before(record.DueAt) ||
		record.Wait == nil || *record.Wait != record.ClaimedAt.Sub(record.DueAt).Milliseconds() {
		return dispatchClaim{}, false, errors.New("invalid dispatch claim")
	}
	record.DueAt, record.ClaimedAt = record.DueAt.UTC(), record.ClaimedAt.UTC()
	return record.dispatchClaim, !record.ClaimedAt.Before(options.from) && record.ClaimedAt.Before(options.until), nil
}

func summarizeDispatchClaims(options dispatchStatsOptions, claims map[dispatchClaimKey]dispatchClaim) dispatchStatsReport {
	cohortWaits := make(map[dispatchCohort][]int64, len(options.expected))
	for cohort := range options.expected {
		cohortWaits[cohort] = nil
	}
	providerWaits := make(map[string][]int64)
	tenantWaits := make(map[string][]int64)
	for _, claim := range claims {
		wait := claim.ClaimedAt.Sub(claim.DueAt).Milliseconds()
		cohortWaits[claim.dispatchCohort] = append(cohortWaits[claim.dispatchCohort], wait)
		providerWaits[claim.Provider] = append(providerWaits[claim.Provider], wait)
		tenantWaits[claim.Tenant] = append(tenantWaits[claim.Tenant], wait)
	}
	report := dispatchStatsReport{
		From: options.from, Until: options.until, Claims: len(claims),
		Providers: make(map[string]dispatchWaitStats, len(providerWaits)), Tenants: make(map[string]dispatchWaitStats, len(tenantWaits)),
		Cohorts: make([]dispatchCohortStats, 0, len(cohortWaits)), Missing: []dispatchCohort{}, OverLimit: []dispatchCohort{},
	}
	if options.maxP95 >= 0 {
		report.MaxP95 = &options.maxP95
	}
	for cloud, waits := range providerWaits {
		report.Providers[cloud] = summarizeDispatchWaits(waits)
	}
	for tenant, waits := range tenantWaits {
		report.Tenants[tenant] = summarizeDispatchWaits(waits)
	}
	for cohort, waits := range cohortWaits {
		report.Cohorts = append(report.Cohorts, dispatchCohortStats{cohort, summarizeDispatchWaits(waits)})
	}
	slices.SortFunc(report.Cohorts, func(a, b dispatchCohortStats) int {
		return cmp.Or(strings.Compare(a.Tenant, b.Tenant), strings.Compare(a.Provider, b.Provider))
	})
	for _, cohort := range report.Cohorts {
		if cohort.Claims == 0 {
			report.Missing = append(report.Missing, cohort.dispatchCohort)
		} else if report.MaxP95 != nil && cohort.P95 > *report.MaxP95 {
			report.OverLimit = append(report.OverLimit, cohort.dispatchCohort)
		}
	}
	report.ThresholdMet = report.Claims > 0 && len(report.Missing) == 0 && len(report.OverLimit) == 0
	return report
}

func summarizeDispatchWaits(waits []int64) dispatchWaitStats {
	count := len(waits)
	if count == 0 {
		return dispatchWaitStats{}
	}
	slices.Sort(waits)
	return dispatchWaitStats{Claims: count, P50: waits[(count+1)/2-1], P95: waits[(count*95+99)/100-1], Max: waits[count-1]}
}
