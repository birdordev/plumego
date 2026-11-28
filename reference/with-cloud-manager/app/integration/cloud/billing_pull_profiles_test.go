package cloudtest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	billingservice "with-cloud-manager/app/services/billing"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestBillingPullPlanMonthWindow(t *testing.T) {
	profile := rollingBillingProfile(provider.TencentCVM, "tencent", "ap-guangzhou", "exports-1250000000")
	profile.MonthWindow.PreviousMonths = 2
	boundary := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	plan, err := billingservice.LoadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profile), nil, boundary.Add(-time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	check := func(period string, exists bool) billingservice.ScheduledBill {
		t.Helper()
		key := profile.Key
		key.Period = period
		job, found := plan.Find(key)
		if found != exists {
			t.Fatalf("period %s present=%t, want %t", period, found, exists)
		}
		return job
	}
	check("2027-01", false)
	check("2026-12", true)
	check("2026-11", true)
	check("2026-10", true)
	old := plan.Sources[0].Bills
	next := boundary.Add(time.Hour)
	old[0].Next = next
	if err := plan.Refresh(boundary); err != nil {
		t.Fatal(err)
	}
	if !check("2027-01", true).Next.IsZero() || !check("2026-12", true).Next.Equal(next) {
		t.Fatal("month rollover did not preserve only the overlapping due time")
	}
	check("2026-11", true)
	check("2026-10", false)
	current := &plan.Sources[0].Bills[0]
	if err := plan.Refresh(boundary.Add(5 * time.Second)); err != nil || &plan.Sources[0].Bills[0] != current {
		t.Fatalf("unexpected month refresh: %v", err)
	}
	if !check("2026-12", true).Next.Equal(next) {
		t.Fatal("same-month refresh reset the due time")
	}
}

func TestExpandBillingObjects(t *testing.T) {
	objects := []billing.PullObject{{Key: "exports/{period}/bill-{compact_period}.zip", Format: "zip",
		Entries: []string{"{compact_period}.csv", "part-{period}.csv", "adjustments.csv"}}}
	expanded, err := billingservice.ExpandBillingObjects(objects, "2027-01", "202701")
	if err != nil || expanded[0].Key != "exports/2027-01/bill-202701.zip" ||
		strings.Join(expanded[0].Entries, ",") != "202701.csv,part-2027-01.csv,adjustments.csv" || objects[0].Entries[0] != "{compact_period}.csv" {
		t.Fatalf("incorrect or destructive expansion: %+v %v", expanded, err)
	}
}

func TestBillingPullPlanRejectsAmbiguousWindows(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	newProfile := func() billingservice.BillingPullProfile {
		return rollingBillingProfile(provider.TencentCVM, "tencent", "ap-guangzhou", "exports-1250000000")
	}
	for _, test := range []struct {
		name   string
		change func(*billingservice.BillingPullProfile)
	}{
		{"both period and window", func(p *billingservice.BillingPullProfile) { p.Period = "2026-10" }},
		{"window too wide", func(p *billingservice.BillingPullProfile) { p.MonthWindow.PreviousMonths = 4 }},
		{"negative lookback", func(p *billingservice.BillingPullProfile) { p.MonthWindow.PreviousMonths = -1 }},
		{"invalid offset", func(p *billingservice.BillingPullProfile) { p.MonthWindow.UTCOffsetMinutes = 841 }},
		{"FOCUS offset mismatch", func(p *billingservice.BillingPullProfile) {
			p.Source, p.Manifest = billing.Focus, billing.Manifest{Provider: provider.TencentCVM}
		}},
		{"object key missing period", func(p *billingservice.BillingPullProfile) { p.Objects[0].Key = "export.csv" }},
		{"unknown template token", func(p *billingservice.BillingPullProfile) { p.Objects[0].Key += "{unexpected}" }},
		{"fixed digest", func(p *billingservice.BillingPullProfile) { p.Objects[0].SHA256 = strings.Repeat("a", 64) }},
		{"fixed control total", func(p *billingservice.BillingPullProfile) { p.Manifest.ExpectedBilled = map[string]string{"CNY": "42"} }},
		{"expanded duplicate key", func(p *billingservice.BillingPullProfile) {
			p.Objects = []billing.PullObject{{Key: "{period}/2026-11.csv", Format: "csv"}, {Key: "2026-11/{period}.csv", Format: "csv"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := newProfile()
			test.change(&profile)
			_, err := billingservice.LoadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profile), nil, now)
			if !errors.Is(err, billingservice.ErrBillingPullProfiles) {
				t.Fatalf("invalid rolling profile accepted: %v", err)
			}
		})
	}
	rolling, fixed := newProfile(), newProfile()
	fixed.MonthWindow, fixed.Period, fixed.Objects[0].Key = nil, "2023-03", "historical.csv"
	for _, profiles := range [][]billingservice.BillingPullProfile{{rolling, fixed}, {fixed, rolling}, {rolling, rolling}, {fixed, fixed}} {
		_, err := billingservice.LoadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, profiles...), nil, now)
		if !errors.Is(err, billingservice.ErrBillingPullProfiles) {
			t.Fatalf("profiles with overlapping scope accepted: %v", err)
		}
	}
	second := fixed
	second.Period = "2023-04"
	if _, err := billingservice.LoadBillingPullPlan(billing.Store{}, writeBillingPullProfiles(t, fixed, second), nil, now); err != nil {
		t.Fatalf("existing fixed periods stopped working: %v", err)
	}
}

func rollingBillingProfile(vendor, account, region, bucket string) billingservice.BillingPullProfile {
	return billingservice.BillingPullProfile{
		PullJob: billing.PullJob{
			Key:        billing.Key{Tenant: "tenant", Project: "finance", Account: account, Source: billing.Native},
			Generation: 1, IntervalSeconds: 300, Region: region, Bucket: bucket,
			Manifest: billing.Manifest{Provider: vendor, Currency: "CNY", Columns: &billing.Columns{Account: "Account", Period: "Month", Billed: "Billed"}},
			Objects:  []billing.PullObject{{Key: "exports/{period}/bill-{compact_period}.csv", Format: "csv"}},
		},
		Credentials: publiccloud.ObjectCredentials{AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret, SecurityToken: fixtureLaunchSession},
		MonthWindow: &billingservice.BillingMonthWindow{UTCOffsetMinutes: 480, PreviousMonths: 1},
	}
}

func writeBillingPullProfiles(t *testing.T, profiles ...billingservice.BillingPullProfile) string {
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
