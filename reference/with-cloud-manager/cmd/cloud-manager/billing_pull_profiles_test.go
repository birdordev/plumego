package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestBillingPullPlanMonthWindow(t *testing.T) {
	profile := rollingBillingProfile(provider.TencentCVM, "tencent", "ap-guangzhou", "exports-1250000000")
	profile.MonthWindow.PreviousMonths = 2
	boundary := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	plan, err := loadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profile), nil, boundary.Add(-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	check := func(period string, exists bool) scheduledBill {
		t.Helper()
		key := profile.Key
		key.Period = period
		job, found := plan.find(key)
		if found != exists {
			t.Fatalf("period %s present=%t, want %t", period, found, exists)
		}
		return job
	}
	check("2027-01", false)
	check("2026-12", true)
	check("2026-11", true)
	check("2026-10", true)
	old := plan.sources[0].bills
	next := boundary.Add(time.Hour)
	old[0].next = next
	if err := plan.refresh(boundary); err != nil {
		t.Fatal(err)
	}
	if !check("2027-01", true).next.IsZero() || !check("2026-12", true).next.Equal(next) {
		t.Fatal("month rollover did not preserve only the overlapping due time")
	}
	check("2026-11", true)
	check("2026-10", false)
	current := &plan.sources[0].bills[0]
	if err := plan.refresh(boundary.Add(5 * time.Second)); err != nil || &plan.sources[0].bills[0] != current {
		t.Fatalf("unexpected month refresh: %v", err)
	}
	if !check("2026-12", true).next.Equal(next) {
		t.Fatal("same-month refresh reset the due time")
	}
}

func TestExpandBillingObjects(t *testing.T) {
	objects := []billing.PullObject{{Key: "exports/{period}/bill-{compact_period}.zip", Format: "zip",
		Entries: []string{"{compact_period}.csv", "part-{period}.csv", "adjustments.csv"}}}
	expanded, err := expandBillingObjects(objects, "2027-01", "202701")
	if err != nil || expanded[0].Key != "exports/2027-01/bill-202701.zip" ||
		strings.Join(expanded[0].Entries, ",") != "202701.csv,part-2027-01.csv,adjustments.csv" || objects[0].Entries[0] != "{compact_period}.csv" {
		t.Fatalf("incorrect or destructive expansion: %+v %v", expanded, err)
	}
}

func TestBillingPullPlanRejectsAmbiguousWindows(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	newProfile := func() billingPullProfile {
		return rollingBillingProfile(provider.TencentCVM, "tencent", "ap-guangzhou", "exports-1250000000")
	}
	for _, test := range []struct {
		name   string
		change func(*billingPullProfile)
	}{
		{"both period and window", func(p *billingPullProfile) { p.Period = "2026-10" }},
		{"window too wide", func(p *billingPullProfile) { p.MonthWindow.PreviousMonths = 4 }},
		{"negative lookback", func(p *billingPullProfile) { p.MonthWindow.PreviousMonths = -1 }},
		{"invalid offset", func(p *billingPullProfile) { p.MonthWindow.UTCOffsetMinutes = 841 }},
		{"FOCUS offset mismatch", func(p *billingPullProfile) {
			p.Source, p.Manifest = billing.Focus, billing.Manifest{Provider: provider.TencentCVM}
		}},
		{"object key missing period", func(p *billingPullProfile) { p.Objects[0].Key = "export.csv" }},
		{"unknown template token", func(p *billingPullProfile) { p.Objects[0].Key += "{unexpected}" }},
		{"fixed digest", func(p *billingPullProfile) { p.Objects[0].SHA256 = strings.Repeat("a", 64) }},
		{"fixed control total", func(p *billingPullProfile) { p.Manifest.ExpectedBilled = map[string]string{"CNY": "42"} }},
		{"expanded duplicate key", func(p *billingPullProfile) {
			p.Objects = []billing.PullObject{{Key: "{period}/2026-11.csv", Format: "csv"}, {Key: "2026-11/{period}.csv", Format: "csv"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := newProfile()
			test.change(&profile)
			_, err := loadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profile), nil, now)
			if !errors.Is(err, errBillingPullProfiles) {
				t.Fatalf("invalid rolling profile accepted: %v", err)
			}
		})
	}
	rolling, fixed := newProfile(), newProfile()
	fixed.MonthWindow, fixed.Period, fixed.Objects[0].Key = nil, "2023-03", "historical.csv"
	for _, profiles := range [][]billingPullProfile{{rolling, fixed}, {fixed, rolling}, {rolling, rolling}, {fixed, fixed}} {
		_, err := loadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profiles...), nil, now)
		if !errors.Is(err, errBillingPullProfiles) {
			t.Fatalf("profiles with overlapping scope accepted: %v", err)
		}
	}
	second := fixed
	second.Period = "2023-04"
	if _, err := loadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, fixed, second), nil, now); err != nil {
		t.Fatalf("existing fixed periods stopped working: %v", err)
	}
}

func rollingBillingProfile(vendor, account, region, bucket string) billingPullProfile {
	return billingPullProfile{
		PullJob: billing.PullJob{
			Key:        billing.Key{Tenant: "tenant", Project: "finance", Account: account, Source: billing.Native},
			Generation: 1, IntervalSeconds: 300, Region: region, Bucket: bucket,
			Manifest: billing.Manifest{Provider: vendor, Currency: "CNY", Columns: &billing.Columns{Account: "Account", Period: "Month", Billed: "Billed"}},
			Objects:  []billing.PullObject{{Key: "exports/{period}/bill-{compact_period}.csv", Format: "csv"}},
		},
		Credentials: publiccloud.ObjectCredentials{AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret, SecurityToken: fixtureLaunchSession},
		MonthWindow: &billingMonthWindow{UTCOffsetMinutes: 480, PreviousMonths: 1},
	}
}

func writeBillingPullProfiles(t *testing.T, profiles ...billingPullProfile) string {
	t.Helper()
	raw, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "billing.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
