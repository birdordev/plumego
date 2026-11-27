package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/provider"
)

func TestBillingPullControllerRollsPriorityCloudMonths(t *testing.T) {
	for _, test := range []struct{ vendor, account, region, bucket, endpoint, format string }{
		{provider.TencentCVM, "tc", "ap-guangzhou", "exports-1250000000", "exports-1250000000.cos.ap-guangzhou.myqcloud.com", "zip"},
		{provider.AlibabaECS, "ali", "cn-hangzhou", "exports-ali", "exports-ali.oss-cn-hangzhou.aliyuncs.com", "csv"},
		{provider.HuaweiECS, "hw", "cn-north-4", "exports-hw", "exports-hw.obs.cn-north-4.myhuaweicloud.com", "gzip"},
	} {
		t.Run(test.vendor, func(t *testing.T) {
			t.Parallel()
			profile := rollingBillingProfile(test.vendor, test.account, test.region, test.bucket)
			profile.Objects[0].Key = "exports/{period}/bill-{compact_period}." + test.format
			profile.Objects[0].Format = test.format
			if test.format == "zip" {
				profile.Objects[0].Entries = []string{"first.csv", "second.csv"}
			}
			verifyPriorityBillingRollover(t, profile, test.endpoint)
		})
	}
}

func verifyPriorityBillingRollover(t *testing.T, profile billingPullProfile, endpointHost string) {
	t.Helper()
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	remote, hwProject := "10001", ""
	if profile.Manifest.Provider == provider.HuaweiECS {
		remote, hwProject = strings.Repeat("a", 32), strings.Repeat("b", 32)
		profile.Manifest.Columns.Project = "Project"
	}
	remoteIdentity := remote
	if hwProject != "" {
		remoteIdentity += ":" + hwProject
	}
	if profile.Manifest.Provider == provider.AlibabaECS {
		profile.Source = billing.Focus
		profile.Manifest.Currency, profile.Manifest.Columns, profile.Manifest.OffsetMinutes = "", nil, 480
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','finance')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_cloud_accounts
		(tenant_id,project_id,account_id,provider,remote_identity,credential_ref,mode)
		VALUES ('tenant','finance',$1,$2,$3,'fixture-reference','read_only')`, profile.Account, profile.Manifest.Provider, remoteIdentity); err != nil {
		t.Fatal(err)
	}
	makeExport := func(period, amount string) []byte {
		csv := "Account,Month,Billed\n" + remote + "," + period + "," + amount + "\n"
		if hwProject != "" {
			csv = "Account,Project,Month,Billed\n" + remote + "," + hwProject + "," + period + "," + amount + "\n"
		}
		if profile.Source == billing.Focus {
			start, err := time.Parse("2006-01", period)
			if err != nil {
				t.Fatal(err)
			}
			begin, end := start.Add(-8*time.Hour).Format(time.RFC3339), start.AddDate(0, 1, 0).Add(-8*time.Hour).Format(time.RFC3339)
			csv = "BillingAccountId,BillingPeriodStart,BillingPeriodEnd,ProviderName,BillingCurrency,BilledCost,EffectiveCost\n" +
				remote + "," + begin + "," + end + ",Alibaba Cloud,CNY," + amount + ",0.08\n"
		}
		return exportFixture(t, profile.Objects[0].Format, []byte(csv))
	}
	const november, december, january = "2026-11", "2026-12", "2027-01"
	decemberBody := makeExport(december, "0.1")
	decemberWrong := makeExport(january, "0.2")
	decemberRestated := makeExport(december, "0.2")
	januaryBody := makeExport(january, "0.2")
	var decemberMode, decemberGET, januaryGET atomic.Int32
	pathFor := func(period string) string {
		return "/exports/" + period + "/bill-" + strings.ReplaceAll(period, "-", "") + "." + profile.Objects[0].Format
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != endpointHost || r.URL.RawQuery != "" || !validBillingObjectSignature(r, profile.Manifest.Provider, profile.Region, profile.Bucket) {
			t.Error("unexpected host or invalid object signature")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body []byte
		var etag string
		var downloads *atomic.Int32
		switch r.URL.Path {
		case pathFor(november):
			w.WriteHeader(http.StatusNotFound)
			return
		case pathFor(december):
			mode := decemberMode.Load()
			body = [][]byte{decemberBody, decemberWrong, decemberRestated}[mode]
			etag, downloads = `"december-`+strconv.Itoa(int(mode))+`"`, &decemberGET
		case pathFor(january):
			body, etag, downloads = januaryBody, `"january-0"`, &januaryGET
		default:
			t.Errorf("unexpected resolved export key %q", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if match := r.Header.Get("If-Match"); (r.Method == http.MethodGet || match != "") && match != etag {
			t.Error("billing download or recheck lost its ETag precondition")
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			downloads.Add(1)
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint: endpoint, transport: server.Client().Transport}
	store, profilePath := billing.NewStore(db), writeBillingPullProfiles(t, profile)
	boundary := time.Date(2026, 12, 31, 16, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(boundary.Add(-time.Second).Unix())
	calendarNow := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	for range 2 {
		startRollingBillingReplica(t, store, profilePath, transport, calendarNow)
	}
	key := func(period string) billing.Key {
		key := profile.Key
		key.Period = period
		return key
	}
	first := waitRollingBill(t, store, key(december), "published")
	waitRollingBill(t, store, key(november), "not_found")
	if _, err := store.PullStatus(ctx, key(january)); !errors.Is(err, billing.ErrNotFound) {
		t.Fatalf("future month ran early: %v", err)
	}
	clock.Store(boundary.Unix())
	rolled := waitRollingBill(t, store, key(january), "published")
	unchanged := waitRollingBill(t, store, key(december), "published")
	if first.AttemptedAt == nil || unchanged.AttemptedAt == nil || !unchanged.AttemptedAt.Equal(*first.AttemptedAt) || decemberGET.Load() != 1 || januaryGET.Load() != 1 {
		t.Fatal("rollover repeated a leased download or reset an overlapping month")
	}
	if original, err := store.PulledObject(ctx, key(january), rolled.Observation, 1); err != nil || !bytes.Equal(original, januaryBody) {
		t.Fatalf("new month archive differs from the source: %v", err)
	}
	var out bytes.Buffer
	decemberMode.Store(1)
	if err := pullConfiguredBill(ctx, store, profilePath, key(december), &out, transport, calendarNow()); err == nil || !strings.Contains(out.String(), `"outcome":"invalid_export"`) {
		t.Fatalf("wrong period was not rejected: %s %v", out.String(), err)
	}
	protected, err := store.Verify(ctx, key(december), 1)
	if err != nil || !protected.Current {
		t.Fatalf("wrong period replaced the published report: %+v %v", protected, err)
	}
	decemberMode.Store(2)
	out.Reset()
	if err := pullConfiguredBill(ctx, store, profilePath, key(december), &out, transport, calendarNow()); err != nil || !strings.Contains(out.String(), `"report_revision":2`) {
		t.Fatalf("previous month could not be restated: %s %v", out.String(), err)
	}
	out.Reset()
	if err := pullConfiguredBill(ctx, store, profilePath, key(november), &out, transport, calendarNow()); err == nil || out.Len() != 0 {
		t.Fatalf("expired period could still be explicitly pulled: %s %v", out.String(), err)
	}
	if old, err := store.VerifyPulledExport(ctx, key(december), first.Observation); err != nil || old.Current || old.Revision != 1 {
		t.Fatalf("historical receipt no longer replays: %+v %v", old, err)
	}
}

func startRollingBillingReplica(t *testing.T, store billing.Store, path string, transport http.RoundTripper, now func() time.Time) {
	t.Helper()
	plan, err := loadBillingPullPlan(store, path, transport, now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runBillingPullController(ctx, plan, now) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("billing pull controller did not stop")
		}
	})
}

func waitRollingBill(t *testing.T, store billing.Store, key billing.Key, outcome string) billing.PullStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var observed billing.PullStatus
	for {
		status, err := store.PullStatus(ctx, key)
		if err == nil {
			observed = status
			if status.Outcome == outcome {
				return status
			}
		} else if !errors.Is(err, billing.ErrNotFound) && ctx.Err() == nil {
			t.Fatalf("reading %s: %v", key.Period, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("period %s wanted %s, last observed %+v", key.Period, outcome, observed)
		case <-ticker.C:
		}
	}
}
