package cloudtest

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	billingservice "with-cloud-manager/app/services/billing"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestBillingPullSignedProviderExports(t *testing.T) {
	db := dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','finance'),('tenant','shared')`); err != nil {
		t.Fatal(err)
	}
	domain, project := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, test := range []struct{ vendor, alias, remote, region, bucket, source, format string }{
		{provider.TencentCVM, "tencent", "10001", "ap-guangzhou", "exports-1250000000", billing.Native, "zip"},
		{provider.AlibabaECS, "alibaba", "10002", "cn-hangzhou", "exports-ali", billing.Focus, "csv"},
		{provider.HuaweiECS, "huawei", domain + ":" + project, "cn-north-4", "exports-huawei", billing.Native, "gzip"},
	} {
		t.Run(test.alias, func(t *testing.T) {
			ctx := t.Context()
			if _, err := db.ExecContext(ctx, `INSERT INTO cmp_cloud_accounts
				(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
				VALUES ('tenant',$1,'finance',$2,$3,'fixture-reference','read_only')`, test.alias, test.vendor, test.remote); err != nil {
				t.Fatal(err)
			}
			manifest := billing.Manifest{Provider: test.vendor, Currency: "CNY",
				Columns: &billing.Columns{Account: "Account", Period: "Month", Billed: "Billed"}}
			header, fields := "Account,Month,Billed\n", test.remote+",2026-11,"
			if test.vendor == provider.HuaweiECS {
				manifest.Columns.Project = "Project"
				header, fields = "Account,Project,Month,Billed\n", domain+","+project+",2026-11,"
			}
			if test.source == billing.Focus {
				manifest.Currency, manifest.Columns, manifest.OffsetMinutes = "", nil, 480
				header = "BillingAccountId,BillingPeriodStart,BillingPeriodEnd,ProviderName,BillingCurrency,BilledCost,EffectiveCost\n"
				fields = "10002,2026-10-31T16:00:00Z,2026-11-30T16:00:00Z,Alibaba Cloud,CNY,"
			}
			makeExport := func(amount string) []byte {
				t.Helper()
				ending := "\n"
				if test.source == billing.Focus {
					ending = ",0.08\n"
				}
				csv := []byte(header + fields + amount + ending)
				return exportFixture(t, test.format, csv)
			}
			var body atomic.Pointer[[]byte]
			baseline := makeExport("0.1")
			next := makeExport("0.2")
			body.Store(&baseline)
			var downloads atomic.Int32
			var mode atomic.Int32
			const object = "2026-11/账单 +分卷#1"
			const encodedObject = "/2026-11/%E8%B4%A6%E5%8D%95%20%2B%E5%88%86%E5%8D%B7%231"
			key := object + "." + test.format
			host := test.bucket + ".cos." + test.region + ".myqcloud.com"
			switch test.vendor {
			case provider.AlibabaECS:
				host = test.bucket + ".oss-" + test.region + ".aliyuncs.com"
			case provider.HuaweiECS:
				host = test.bucket + ".obs." + test.region + ".myhuaweicloud.com"
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != host || r.URL.Path != "/"+key || r.URL.RawQuery != "" || r.URL.EscapedPath() != encodedObject+"."+test.format || r.Header.Get("Accept-Encoding") != "identity" ||
					!validBillingObjectSignature(r, test.vendor, test.region, test.bucket) {
					t.Error("unexpected billing object request or signature")
					w.WriteHeader(400)
					return
				}
				value := *body.Load()
				etag := `"revision-1"`
				if bytes.Equal(value, next) {
					etag = `"revision-2"`
				}
				if r.Method == http.MethodGet {
					downloads.Add(1)
					if r.Header.Get("If-Match") != etag {
						t.Error("unfenced object GET")
						w.WriteHeader(412)
						return
					}
					if mode.Load() == 1 {
						w.WriteHeader(412)
						return
					}
				} else if r.Method != http.MethodHead {
					t.Error("unexpected object method")
					w.WriteHeader(400)
					return
				}
				if mode.Load() == 2 {
					w.Header().Set("Location", "https://unexpected.example.test/bill")
					w.WriteHeader(302)
					return
				}
				if mode.Load() == 3 && r.Method == http.MethodHead && r.Header.Get("If-Match") != "" {
					w.WriteHeader(412)
					return
				}
				w.Header().Set("ETag", etag)
				w.Header().Set("Content-Length", strconv.Itoa(len(value)))
				w.WriteHeader(http.StatusOK)
				if r.Method == http.MethodGet {
					if mode.Load() == 4 {
						value = value[:len(value)-1]
					}
					_, _ = w.Write(value)
				}
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := cloudFixtureTransport{endpoint: endpoint, transport: server.Client().Transport}
			scope := billing.Key{Tenant: "tenant", Project: "finance", Account: test.alias, Period: "2026-11", Source: test.source}
			profile := billingservice.BillingPullProfile{PullJob: billing.PullJob{Key: scope, Generation: 1, IntervalSeconds: 300,
				Bucket: test.bucket, Region: test.region, Manifest: manifest, Objects: []billing.PullObject{{Key: key, Format: test.format}}},
				Credentials: publiccloud.ObjectCredentials{AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret, SecurityToken: fixtureLaunchSession}}
			if test.format == "zip" {
				profile.Objects[0].Entries = []string{"second.csv", "first.csv"}
			}
			raw, err := json.Marshal([]billingservice.BillingPullProfile{profile})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "billing.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"billing-pull", scope.Tenant, scope.Project, scope.Account, scope.Period, scope.Source}
			var out bytes.Buffer
			pull := func(want string, succeed bool) billing.PullStatus {
				t.Helper()
				out.Reset()
				err := billingservice.RunBillingPull(ctx, db, args, path, &out, transport)
				var receipt billing.PullStatus
				if decodeErr := json.Unmarshal(out.Bytes(), &receipt); decodeErr != nil || receipt.Outcome != want || (err == nil) != succeed {
					t.Fatalf("pull outcome: %+v, %v (%v)", receipt, decodeErr, err)
				}
				if strings.Contains(out.String(), fixtureLaunchSecret) || strings.Contains(out.String(), key) {
					t.Fatal("unsafe billing status")
				}
				return receipt
			}
			first := startBillingFixtureController(t, db, path, scope, transport)
			if first.ReportRevision == nil || *first.ReportRevision != 1 || downloads.Load() != 1 {
				t.Fatalf("bad first report: %+v", first)
			}
			pull("unchanged", true)
			if downloads.Load() != 1 {
				t.Fatal("unchanged export was downloaded")
			}
			out.Reset()
			source := append([]string{"billing-pull-source"}, args[1:]...)
			source = append(source, first.Observation, "1")
			if err := billingservice.RunBillingPull(ctx, db, source, "", &out, nil); err != nil || !bytes.Equal(out.Bytes(), baseline) {
				t.Fatalf("archive changed original bytes: %v", err)
			}
			out.Reset()
			replay := append([]string{"billing-pull-replay"}, args[1:]...)
			replay = append(replay, first.Observation)
			if err := billingservice.RunBillingPull(ctx, db, replay, "", &out, nil); err != nil || !strings.Contains(out.String(), `"revision":1`) {
				t.Fatalf("compressed source replay: %s %v", out.String(), err)
			}
			body.Store(&next)
			for i, outcome := range []string{"object_changed", "invalid_export", "object_changed", "transport_failed"} {
				mode.Store(int32(i + 1))
				receipt := pull(outcome, false)
				if receipt.ReportRevision == nil || *receipt.ReportRevision != 1 || receipt.Observation != first.Observation ||
					receipt.Failures != i+1 || receipt.NextAt.Before(time.Now().Add(time.Minute)) {
					t.Fatalf("failure changed ledger or ignored retry delay: %+v", receipt)
				}
			}
			mode.Store(0)
			second := pull("published", true)
			if second.ReportRevision == nil || *second.ReportRevision != 2 {
				t.Fatalf("new version not published: %+v", second)
			}
			body.Store(&baseline)
			old := pull("historical", true)
			if old.ReportRevision == nil || *old.ReportRevision != 1 {
				t.Fatalf("old export was not recognized: %+v", old)
			}
			ledger := billing.NewStore(db)
			report, err := ledger.Verify(ctx, scope, 2)
			if err != nil || !report.Current || report.Totals[0].Billed == "0.100000000000" {
				t.Fatalf("current report incorrect after replay: %+v %v", report, err)
			}
			scope.Project = "shared"
			if _, err := ledger.PulledObject(ctx, scope, first.Observation, 1); err == nil {
				t.Fatal("archive exposed to a different project")
			}
			scope.Project = "finance"
			if _, err := db.ExecContext(ctx, "UPDATE cmp_billing_pull_objects SET raw=decode('00','hex') WHERE observation=$1", first.Observation); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.VerifyPulledExport(ctx, scope, first.Observation); !errors.Is(err, billing.ErrCorrupt) {
				t.Fatalf("damaged vendor archive passed replay: %v", err)
			}
		})
	}
}

func startBillingFixtureController(t *testing.T, db *sql.DB, path string, scope billing.Key, transport http.RoundTripper) billing.PullStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- billingservice.RunBillingPull(ctx, db, []string{"billing-pull-controller"}, path, io.Discard, transport)
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	store := billing.NewStore(db)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := store.PullStatus(ctx, scope)
		if err == nil && status.Outcome == "published" {
			return status
		}
		select {
		case <-ctx.Done():
			t.Fatalf("controller did not publish: %+v %v", status, err)
		case <-ticker.C:
		}
	}
}

func validBillingObjectSignature(r *http.Request, vendor, region, bucket string) bool {
	switch vendor {
	case provider.TencentCVM:
		return validFixtureCOSSignature(r, "")
	case provider.AlibabaECS:
		return validFixtureOSSSignature(r, region, "/"+bucket+r.URL.EscapedPath())
	case provider.HuaweiECS:
		date := r.Header.Get("Date")
		stamp, err := http.ParseTime(date)
		if err != nil || time.Since(stamp).Abs() > time.Minute || r.Header.Get("X-Obs-Security-Token") != fixtureLaunchSession {
			return false
		}
		signed := fmt.Sprintf("%s\n\n\n%s\nx-obs-security-token:%s\n/%s%s", r.Method, date, fixtureLaunchSession, bucket, r.URL.EscapedPath())
		signature := base64.StdEncoding.EncodeToString(fixtureBucketMAC(sha1.New, []byte(fixtureLaunchSecret), signed))
		return hmac.Equal([]byte("OBS "+fixtureLaunchKey+":"+signature), []byte(r.Header.Get("Authorization")))
	}
	return false
}

func exportFixture(t *testing.T, format string, csv []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	switch format {
	case "zip":
		archive := zip.NewWriter(&raw)
		for i, name := range []string{"first.csv", "second.csv", "readme.txt"} {
			file, err := archive.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			content := csv
			if i == 1 {
				content = bytes.Replace(csv, []byte(",0.1\n"), []byte(",0.05\n"), 1)
				content = bytes.Replace(content, []byte(",0.2\n"), []byte(",0.10\n"), 1)
			}
			if _, err := file.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	case "gzip":
		archive := gzip.NewWriter(&raw)
		if _, err := archive.Write(csv); err != nil {
			t.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		return csv
	}
	return raw.Bytes()
}
