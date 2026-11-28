package apitest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceInventoryDispatchStats(t *testing.T) {
	start := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	var logs bytes.Buffer
	logger := cloud.InventoryDispatchLogger(&logs)
	fixture := controller.DispatchObservation{Tenant: "busy", Account: "shared", Provider: provider.TencentCVM, Kind: "instance", ScheduleID: math.MaxInt64}
	for i := 20; i > 0; i-- {
		fixture.Fence = int64(i)
		fixture.ClaimedAt = start.Add(time.Duration(i-1) * time.Millisecond)
		fixture.DueAt = fixture.ClaimedAt.Add(-time.Duration(i) * time.Millisecond)
		logger(fixture)
	}
	// 另一副本重复采集窗口首条，日志时间不同也应只计一次。
	logger(fixture)
	fixture.ScheduleID--
	fixture.Tenant, fixture.Provider = "quiet", provider.AlibabaECS
	fixture.DueAt, fixture.ClaimedAt = start, start.Add(3*time.Millisecond)
	logger(fixture)
	fixture.ScheduleID--
	fixture.Provider = provider.HuaweiECS
	fixture.DueAt, fixture.ClaimedAt = start, start
	logger(fixture)
	fixture.Tenant, fixture.DueAt, fixture.ClaimedAt = "outside", end, end
	logger(fixture)
	fixture.DueAt, fixture.ClaimedAt = start.Add(-time.Nanosecond), start.Add(-time.Nanosecond)
	logger(fixture)
	logs.WriteString(`{"msg":"other","detail":"private log payload"}` + "\n")

	args := []string{"inventory-dispatch-stats", "--from", "2026-11-28T16:00:00+08:00", "--until", end.Format(time.RFC3339),
		"--max-p95-ms", "19", "--expect", "quiet/huawei-ecs", "--expect", "quiet/alibaba-ecs"}
	var out bytes.Buffer
	err := commands.RunGovernanceClient(t.Context(), args, "unused-address", "", bytes.NewReader(logs.Bytes()), &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	var report commands.DispatchStatsReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	busy := commands.DispatchWaitStats{Claims: 20, P50: 10, P95: 19, Max: 20}
	quiet := commands.DispatchWaitStats{Claims: 2, P50: 0, P95: 3, Max: 3}
	if !report.ThresholdMet || report.Claims != 22 || report.Duplicates != 1 || !report.From.Equal(start) || !report.Until.Equal(end) ||
		report.InputDigest != fmt.Sprintf("%x", sha256.Sum256(logs.Bytes())) || len(report.Providers) != 3 || len(report.Tenants) != 2 ||
		report.Providers[provider.TencentCVM] != busy || report.Tenants["busy"] != busy || report.Tenants["quiet"] != quiet ||
		report.Providers[provider.HuaweiECS] != (commands.DispatchWaitStats{Claims: 1}) || len(report.Missing) != 0 || len(report.OverLimit) != 0 {
		t.Fatalf("unexpected dispatch statistics: %s", &out)
	}
	if out.Len() == 0 || strings.Contains(out.String(), "private log payload") || strings.Contains(out.String(), `"account"`) || strings.Contains(out.String(), "schedule_id") {
		t.Fatalf("report exposed raw log details: %s", &out)
	}
	want := []commands.DispatchCohortStats{
		{DispatchCohort: commands.DispatchCohort{Tenant: "busy", Provider: provider.TencentCVM}, DispatchWaitStats: busy},
		{DispatchCohort: commands.DispatchCohort{Tenant: "quiet", Provider: provider.AlibabaECS}, DispatchWaitStats: commands.DispatchWaitStats{Claims: 1, P50: 3, P95: 3, Max: 3}},
		{DispatchCohort: commands.DispatchCohort{Tenant: "quiet", Provider: provider.HuaweiECS}, DispatchWaitStats: commands.DispatchWaitStats{Claims: 1}},
	}
	if !slices.Equal(report.Cohorts, want) {
		t.Fatalf("unexpected cohort order or values: %+v", report.Cohorts)
	}
	out.Reset()
	args[6] = "18"
	args = append(args, "--expect", "missing/tencent-cvm")
	err = commands.RunGovernanceClient(t.Context(), args, "", "", bytes.NewReader(logs.Bytes()), &out, nil)
	if err == nil || json.Unmarshal(out.Bytes(), &report) != nil || report.ThresholdMet ||
		!slices.Equal(report.Missing, []commands.DispatchCohort{{Tenant: "missing", Provider: provider.TencentCVM}}) ||
		!slices.Equal(report.OverLimit, []commands.DispatchCohort{{Tenant: "busy", Provider: provider.TencentCVM}}) ||
		len(report.Cohorts) != 4 || report.Cohorts[1].Claims != 0 {
		t.Fatalf("did not report missing and slow cohorts: %s, %v", &out, err)
	}
}

func TestInventoryDispatchStatsRejectsInvalidEvidence(t *testing.T) {
	var log bytes.Buffer
	at := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	cloud.InventoryDispatchLogger(&log)(controller.DispatchObservation{
		Tenant: "lab", Account: "account", Provider: provider.TencentCVM, Kind: "instance",
		ScheduleID: 3, Fence: 7, DueAt: at.Add(-time.Millisecond), ClaimedAt: at,
	})
	valid := log.String()
	args := []string{"--from", at.Format(time.RFC3339), "--until", at.Add(time.Minute).Format(time.RFC3339)}
	for name, input := range map[string]string{
		"future schema": strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		"missing wait":  strings.Replace(valid, `"due_wait_ms":1,`, "", 1),
		"forged wait":   strings.Replace(valid, `"due_wait_ms":1`, `"due_wait_ms":0`, 1),
		"nonpositive":   strings.Replace(valid, `"fence":7`, `"fence":0`, 1),
		"conflict":      valid + strings.Replace(valid, `"account":"account"`, `"account":"other"`, 1),
		"truncated":     valid + `{"msg":"inventory_dispatch_claimed"`,
		"oversized":     valid + strings.Repeat("x", 1<<16),
	} {
		t.Run(name, func(t *testing.T) {
			var report bytes.Buffer
			if err := commands.RunDispatchStats(t.Context(), args, strings.NewReader(input), &report); err == nil || report.Len() != 0 {
				t.Fatalf("invalid evidence produced report: %s, %v", &report, err)
			}
		})
	}

	var out bytes.Buffer
	err := commands.RunDispatchStats(t.Context(), args, strings.NewReader(`{"msg":"other"}`), &out)
	var empty commands.DispatchStatsReport
	if err == nil || json.Unmarshal(out.Bytes(), &empty) != nil || empty.ThresholdMet || empty.Claims != 0 {
		t.Fatalf("empty evidence was not reported as failing: %s, %v", &out, err)
	}
	for _, invalid := range [][]string{
		{"--from", "not-a-date", "--until", args[3]},
		{"--from", args[3], "--until", args[1]},
		append(slices.Clone(args), "--expect", "tenant/one/provider"),
		append(slices.Clone(args), "--expect", "tenant/provider", "--expect", "tenant/provider"),
		append(slices.Clone(args), "--max-p95-ms", "-2"),
	} {
		out.Reset()
		if err := commands.RunDispatchStats(t.Context(), invalid, strings.NewReader(valid), &out); err == nil || out.Len() != 0 {
			t.Fatalf("accepted invalid arguments %v: %s, %v", invalid, &out, err)
		}
	}
}
