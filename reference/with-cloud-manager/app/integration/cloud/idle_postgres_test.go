package cloudtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptanceIdleAnalysis(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const token = "fixture-idle-viewer-project-test"
	hash := sha256.Sum256([]byte(token))
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('external');
		INSERT INTO cmp_projects VALUES ('tenant','ops'),('tenant','other'),('external','ops');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','viewer','service','fixture','viewer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
			('tenant','ops','viewer','viewer'),('tenant','other','viewer','viewer');`)
	exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','viewer',clock_timestamp()+interval '1 hour')`, hash[:])
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	for _, row := range []struct {
		vendor, alias, account, region, id string
		peak                               float64
		samples, calls                     int
	}{
		{provider.TencentCVM, "tx", "12001", "ap-guangzhou", "ins-idle", 5000, 168, 5},
		{provider.AlibabaECS, "ali", "12002", "cn-hangzhou", "i-idle", 4000, 1008, 3},
		{provider.HuaweiECS, "hw", strings.Repeat("a", 32), "cn-north-4", "f2111133-7788-49ab-8ee8-aa111111111a", 1000, 168, 1},
	} {
		t.Run(row.alias, func(t *testing.T) {
			f := &idleCloudFixture{vpcCreationFixture: vpcCreationFixture{vendor: row.vendor, region: row.region, account: row.account}, instance: row.id}
			ref := "idle/" + row.alias
			exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
				VALUES ('tenant',$1,'ops',$2,$3,$4,'read_only')`, row.alias, row.vendor, f.remoteIdentity(), ref)
			exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,running,lease_until,completed_at)
				VALUES ('tenant',$1,$2,'instance',1,false,clock_timestamp(),clock_timestamp())`, row.alias, row.region)
			exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
				SELECT scope_id,$2,'fixture instance','running',1 FROM cmp_inventory_scopes WHERE account_id=$1`, row.alias, row.id)
			transport, profiles := startIdleFixture(t, f, ref)
			schedule := []string{"idle-schedule", "tenant", row.alias, row.region, row.id, "24h"}
			if !cloudmanager.ValidCommand(schedule) || !cloudmanager.ValidCommand([]string{"idle-controller"}) {
				t.Fatal("idle commands unavailable")
			}
			if err := cloud.RunIdleCommand(t.Context(), db, schedule, "", nil); err != nil {
				t.Fatal(err)
			}
			query := "/v1/tenants/tenant/projects/ops/governance/idle-instances?account=" + row.alias
			get := func(path string, status int) inventory.IdleInstances {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				app.ServeHTTP(w, req)
				if w.Code != status || strings.Contains(w.Body.String(), fixtureHuaweiIAM) || strings.Contains(w.Body.String(), fixtureLaunchSecret) {
					t.Fatalf("idle report: %d want %d, %s", w.Code, status, w.Body)
				}
				var result struct{ Data inventory.IdleInstances }
				if status == 200 {
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
				}
				return result.Data
			}
			current := func(state, reason string) inventory.IdleInstance {
				t.Helper()
				page := get(query, 200)
				if len(page.Items) != 1 || page.Items[0].State != state || page.Items[0].Reason != reason ||
					page.CPUThresholdPercent != 5 || page.NetworkThresholdBytesPerSec != 65536 {
					t.Fatalf("idle report: %+v", page)
				}
				return page.Items[0]
			}
			current("insufficient", "metrics_missing")
			get(strings.Replace(query, "tenants/tenant/", "tenants/external/", 1), 403)
			if items := get(strings.Replace(query, "/ops/", "/other/", 1), 200).Items; len(items) != 0 {
				t.Fatalf("other project can read the evidence: %+v", items)
			}
			start := func(mode int32, expected string, delay int) {
				t.Helper()
				f.mode.Store(mode)
				exec(`UPDATE cmp_inventory_schedules SET next_at=clock_timestamp(),last_finished_at=NULL WHERE account_id=$1 AND kind='instance-idle'`, row.alias)
				runIdleFixtureCycle(t, db, profiles, transport, row.alias, expected, delay)
			}
			start(0, "success", 23*3600)
			candidate := current("candidate", "")
			if candidate.Evidence == nil || candidate.Evidence.Expected != row.samples ||
				candidate.Evidence.CPU != row.samples || candidate.Evidence.Network != row.samples ||
				candidate.Evidence.CPUPeak == nil || *candidate.Evidence.CPUPeak != 2 ||
				candidate.Evidence.NetworkPeak == nil || *candidate.Evidence.NetworkPeak != row.peak ||
				len(candidate.Evidence.EvidenceSHA256) != 64 || f.calls.Load() != int32(row.calls) {
				t.Fatalf("wrong normalized candidate: %+v", candidate.Evidence)
			}
			var out bytes.Buffer
			args := []string{"idle", "tenant", "ops", "--account", row.alias, "--state", "candidate"}
			if err := commands.RunGovernanceClient(ctx, args, server.URL, token, nil, &out, nil); err != nil || !strings.Contains(out.String(), candidate.Evidence.EvidenceSHA256) {
				t.Fatalf("idle CLI failed: %s %v", out.String(), err)
			}
			start(idleForeign, "failure", 55)
			start(idleThrottled, "throttled", 75)
			if current("candidate", "").Evidence.EvidenceSHA256 != candidate.Evidence.EvidenceSHA256 {
				t.Fatal("failed collection changed previously verified evidence")
			}
			if row.alias == "tx" {
				exec(`UPDATE cmp_inventory_schedules SET failures=3,last_outcome='failure' WHERE kind='instance-idle';
					UPDATE cmp_project_alert_checks SET next_at=clock_timestamp()+interval '1 hour';
					UPDATE cmp_project_alert_checks SET next_at=clock_timestamp() WHERE (tenant_id,project_id)=('tenant','ops')`)
				if worked, err := alerts.NewStore(db).EvaluateNext(ctx); err != nil || !worked {
					t.Fatalf("alert evaluation: %v %v", worked, err)
				}
				var num int
				if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_project_alerts WHERE resource_kind='instance-idle'").Scan(&num); err != nil || num != 0 {
					t.Fatalf("idle analysis polluted inventory scan alerts: %d %v", num, err)
				}
			}
			start(idlePartial, "success", 23*3600)
			if evidence := current("insufficient", "incomplete_metrics").Evidence; evidence == nil ||
				evidence.Network >= evidence.Expected || evidence.CPU != evidence.Expected {
				t.Fatalf("missing metric was hidden: %+v", evidence)
			}
			if gaps := get(query+"&state=insufficient", 200).Items; len(gaps) != 1 || gaps[0].Reason != "incomplete_metrics" {
				t.Fatalf("sparse filter disagrees with unfiltered report: %+v", gaps)
			}
			start(idleActive, "success", 23*3600)
			current("active", "")
			if row.alias == "tx" {
				key := inventory.Key{Tenant: "tenant", Account: row.alias, Region: row.region, Kind: provider.Instance}
				publish, err := inventory.NewStore(db).PrepareIdleAnalysis(ctx, key, row.id, ref, 1,
					func(_ context.Context, _ provider.Scope, _ string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
						count := int(window.Until.Sub(window.From) / time.Hour)
						return provider.InstanceUtilization{Period: time.Hour,
							CPU: make([]provider.MetricValue, count), Network: make([]provider.MetricValue, count)}, nil
					})
				if err != nil || publish == nil {
					t.Fatalf("preparing evidence for ownership race: %v", err)
				}
				exec(`INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant','tx','other')`)
				exec(`INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
					SELECT 'tenant',scope_id,$1,'other',2 FROM cmp_inventory_scopes WHERE account_id='tx'`, row.id)
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := publish(ctx, tx); !errors.Is(err, inventory.ErrStale) {
					_ = tx.Rollback()
					t.Fatalf("old owner evidence could publish: %v", err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				other := get(strings.Replace(query, "/ops/", "/other/", 1), 200).Items
				if len(other) != 1 || other[0].Reason != "binding_changed" || other[0].Evidence != nil || len(get(query, 200).Items) != 0 {
					t.Fatalf("previous owner evidence is visible after transfer: %+v", other)
				}
				exec(`UPDATE cmp_asset_ownership SET project_id='ops',revision=3 WHERE external_id=$1`, row.id)
			}
			exec(`UPDATE cmp_inventory_objects SET status='stopped' WHERE external_id=$1`, row.id)
			current("not_running", "instance_not_running")
			before := f.calls.Load()
			var lastGood time.Time
			const lastSuccess = `SELECT last_success_at FROM cmp_inventory_schedules WHERE account_id=$1 AND kind='instance-idle'`
			if err := db.QueryRowContext(ctx, lastSuccess, row.alias).Scan(&lastGood); err != nil {
				t.Fatal(err)
			}
			start(0, "success", 23*3600)
			var afterSkip time.Time
			if err := db.QueryRowContext(ctx, lastSuccess, row.alias).Scan(&afterSkip); err != nil || !afterSkip.Equal(lastGood) || f.calls.Load() != before {
				t.Fatalf("a stopped instance collected or advanced last success: %v", err)
			}
			exec(`UPDATE cmp_inventory_objects SET status='running' WHERE external_id=$1`, row.id)
			current("insufficient", "metrics_missing")
			start(0, "success", 23*3600)
			exec(`UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1 WHERE account_id=$1`, row.alias)
			if current("insufficient", "inventory_stale").Evidence != nil {
				t.Fatal("old credential evidence was exposed")
			}
			if row.alias == "hw" {
				base, _, _ := strings.Cut(query, "?")
				page := get(base+"?state=insufficient&limit=1", 200)
				if len(page.Items) != 1 || page.Items[0].Account != "ali" || page.Next == "" {
					t.Fatalf("invalid first sparse page: %+v", page)
				}
				follow := get(base+"?state=insufficient&limit=1&cursor="+url.QueryEscape(page.Next), 200)
				if len(follow.Items) != 1 || follow.Items[0].Account != "hw" || follow.Items[0].Evidence != nil {
					t.Fatalf("scope cursor failed: %+v", follow)
				}
			}
			off := append(append([]string(nil), schedule[:5]...), "off")
			if err := cloud.RunIdleCommand(t.Context(), db, off, "", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func startIdleFixture(t *testing.T, f *idleCloudFixture, ref string) (http.RoundTripper, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{target, server.Client().Transport}
	config := cloud.PublicCloudProfile{Ref: ref, Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	dir := t.TempDir()
	profile, profiles := filepath.Join(dir, "profile.json"), filepath.Join(dir, "profiles.json")
	for path, data := range map[string]any{profile: config, profiles: map[string]string{ref: profile}} {
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return transport, profiles
}

func runIdleFixtureCycle(t *testing.T, db *sql.DB, path string, transport http.RoundTripper, account, expected string, minimumDelay int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cloud.RunIdleCommand(ctx, db, []string{"idle-controller"}, path, transport) }()
	now := time.Now()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result string
		var next time.Time
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(last_outcome,''),next_at FROM cmp_inventory_schedules
			WHERE account_id=$1 AND kind='instance-idle' AND last_finished_at IS NOT NULL`, account).Scan(&result, &next); err == nil {
			if result != expected || next.Sub(now).Seconds() < float64(minimumDelay) {
				t.Fatalf("schedule outcome %s delay %s, expected %s after %s seconds", result, next.Sub(now), expected, strconv.Itoa(minimumDelay))
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return
		}
		select {
		case err := <-done:
			t.Fatalf("idle controller exited before publishing: %v", err)
		case <-ctx.Done():
			t.Fatal("idle controller timed out")
		case <-ticker.C:
		}
	}
}
