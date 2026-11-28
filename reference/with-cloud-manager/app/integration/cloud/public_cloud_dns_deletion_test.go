package cloudtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

type dnsDeletionFixture struct {
	*dnsStatusFixture
	deletions           atomic.Int32
	latePages           atomic.Int32
	attachedHealthCheck atomic.Bool
}

func TestDNSDeletionOperationRequest(t *testing.T) {
	base, err := url.Parse("https://cloud-manager.example")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"delete-dns-record", "tenant", "project", "--idempotency-key", "once"}
	if _, err := commands.PlanOperationRequest(base, args); err == nil {
		t.Fatal("record deletion can omit the revision")
	}
	plan, err := commands.PlanOperationRequest(base, append(args, "--if-match", "1"))
	if err != nil || plan.Method != http.MethodPost || plan.Target.String() != "https://cloud-manager.example/v1/tenants/tenant/projects/project/dns-zones/record-deletion" {
		t.Fatalf("wrong deletion request: %+v %v", plan, err)
	}
}

func TestAcceptancePublicCloudDNSDeletion(t *testing.T) {
	for _, status := range dnsStatusFixtures() {
		f := &dnsDeletionFixture{dnsStatusFixture: status}
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDNSDeletion(t, f)
		})
	}
}

func testPublicCloudDNSDeletion(t *testing.T, f *dnsDeletionFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor = "fixture-public-dns-deletion-operator"
	const reviewer = "fixture-public-dns-deletion-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET role='admin' WHERE principal_id='operator'`); err != nil {
		t.Fatal(err)
	}
	config := cloud.PublicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveDeletion(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	if err := cloud.RunPublicCloud(ctx, db, []string{"scan-" + f.command, "tenant", f.vendor, "global", "dns-zone"}, f.vendor, path, transport); err != nil {
		t.Fatal(err)
	}
	refresh := func() error {
		return cloud.RunPublicCloud(ctx, db, []string{"refresh-" + f.command + "-dns-zone", "tenant", f.vendor, f.id}, f.vendor, path, transport)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const root = "/v1/tenants/tenant/projects/"
	recordsPath := root + "project/assets/dns-zones/" + f.id + "/records?account=" + f.vendor + "&offset=100"
	request := func(method, path, token, key, rev string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if rev != "" {
			r.Header.Set("If-Match", rev)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s returned %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	var snapshot inventory.DNSRecordsPage
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK), &snapshot)
	if len(snapshot.Records) != 1 || snapshot.Records[0].Type != "A" || snapshot.Records[0].Status == "disable" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	body := func(observed time.Time) []byte {
		t.Helper()
		value, err := json.Marshal(operations.DNSRecordDeletionInput{Account: f.vendor, AssetID: f.id, RecordID: f.recordID(), ObservedAt: observed})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	submit := func(project, key string, observed time.Time, status int) operations.Operation {
		t.Helper()
		w := request(http.MethodPost, root+project+"/dns-zones/record-deletion", actor, key, `"1"`, body(observed), status)
		var op operations.Operation
		if status == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.Action != "delete-dns-record" || w.Header().Get("Location") != root+project+"/operations/"+op.ID {
				t.Fatalf("wrong operation receipt: %+v", op)
			}
		}
		return op
	}
	submit("project", "still-enabled", snapshot.ObservedAt, http.StatusBadRequest)
	previous := snapshot.ObservedAt
	f.lastDisabled.Store(true)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK), &snapshot)
	submit("project", "stale-snapshot", previous, http.StatusPreconditionFailed)
	submit("other", "wrong-project", snapshot.ObservedAt, http.StatusForbidden)

	store := operations.NewStore(db)
	var writeAccess, interruptedAttempt atomic.Bool
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if f.latePages.Load() == 5 && interruptedAttempt.CompareAndSwap(false, true) {
				return nil, &provider.Failure{Kind: provider.Transient}
			}
			profile := config.Config
			profile.AllowDNSRecordDeletion = writeAccess.Load()
			client, err := publiccloud.Connect(ctx, binding.Provider, profile, transport)
			if err != nil {
				return nil, err
			}
			if client.Identity() != binding.RemoteIdentity {
				return nil, identity.ErrForbidden
			}
			return client.InstanceOperator(), nil
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, state, true)
	}
	observedAt := snapshot.ObservedAt
	disabled := submit("project", "write-disabled", observedAt, http.StatusAccepted)
	if _, err := store.Approve(ctx, actor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval returned %v", err)
	}
	approve(disabled)
	if stopped := wait(disabled, "failed"); stopped.OutcomeReason != "dns_record_deletion_disabled" || f.deletions.Load() != 0 {
		t.Fatalf("disabled profile wrote remotely: %+v", stopped)
	}

	writeAccess.Store(true)
	f.lastDisabled.Store(false)
	stale := submit("project", "reenabled-remotely", observedAt, http.StatusAccepted)
	approve(stale)
	if stopped := wait(stale, "failed"); stopped.OutcomeReason != "dns_record_deletion_preconditions_unconfirmed" || f.deletions.Load() != 0 {
		t.Fatalf("re-enabled record reached write: %+v", stopped)
	}

	f.lastDisabled.Store(true)
	if f.vendor == provider.HuaweiECS {
		f.attachedHealthCheck.Store(true)
		linked := submit("project", "health-check-attached", observedAt, http.StatusAccepted)
		approve(linked)
		if stopped := wait(linked, "failed"); stopped.OutcomeReason != "dns_record_deletion_preconditions_unconfirmed" || f.deletions.Load() != 0 {
			t.Fatalf("record with health check reached write: %+v", stopped)
		}
		f.attachedHealthCheck.Store(false)
	}
	op := submit("project", "delete-once", observedAt, http.StatusAccepted)
	if replay := submit("project", "delete-once", observedAt, http.StatusAccepted); replay.ID != op.ID {
		t.Fatal("retry created a second operation")
	}
	statusInput := []byte(fmt.Sprintf(`{"account":%q,"asset_id":%q,"record_id":%q,"observed_at":%q,"enabled":true}`,
		f.vendor, f.id, f.recordID(), observedAt.Format(time.RFC3339Nano)))
	request(http.MethodPost, root+"project/dns-zones/record-status", actor, "same-zone-status", `"1"`, statusInput, http.StatusConflict)
	var approved operations.Operation
	testsupport.DecodeProjectData(t, request(http.MethodGet, root+"project/operations/"+op.ID, reviewer, "", "", nil, http.StatusOK), &approved)
	intent := approved.DNSRecordDeletionIntent
	if intent == nil || intent.ZoneName != "example.test" || intent.Record.ID != f.recordID() ||
		intent.Record.Status != "disable" || len(intent.Record.Values) != 1 || intent.Record.Values[0] != "192.0.2.42" {
		t.Fatalf("approval missing exact DNS record: %+v", intent)
	}
	approve(op)

	paused := wait(op, "needs_review")
	if paused.OutcomeReason != "dns_record_deletion_requires_evidence" || f.deletions.Load() != 1 || f.latePages.Load() != 4 {
		t.Fatalf("incomplete pagination ended incorrectly: %+v writes=%d attempts=%d", paused, f.deletions.Load(), f.latePages.Load())
	}
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/asserted"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed cloud evidence: %v", err)
	}
	request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK)
	var count int
	if err := db.QueryRowContext(ctx, `SELECT absence_observations FROM cmp_dns_record_deletion_intents WHERE operation_id=$1`, op.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed scan did not clear evidence: count=%d err=%v", count, err)
	}

	writeAccess.Store(false)
	inv := inventory.NewStore(db)
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: provider.DNSGlobalScope, Kind: provider.DNSZone}
	err = inv.RefreshDNSRecords(ctx, key, f.id, 1, dnsTestReaderFunc(func(context.Context, provider.Scope, provider.DNSZoneRef) ([]provider.DNSRecord, error) {
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/read-only-recheck"}); err != nil {
			return nil, err
		}
		confirmed := wait(op, "succeeded")
		if confirmed.OutcomeReason != "dns_record_absence_confirmed" || f.deletions.Load() != 1 || f.latePages.Load() != 7 || !interruptedAttempt.Load() {
			t.Fatalf("deletion was not confirmed twice without resubmission: %+v writes=%d reads=%d", confirmed, f.deletions.Load(), f.latePages.Load())
		}
		return snapshot.Records, nil
	}))
	if !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("late snapshot write returned %v", err)
	}
	request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusNotFound)
	var events, reservations int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON (o.sequence=e.event_id)
			WHERE o.event_type='dns-zone.operation.succeeded' AND e.operation_id=$1),
		(SELECT count(*) FROM cmp_dns_zone_change_reservations)`, op.ID).Scan(&events, &reservations); err != nil ||
		events != 1 || reservations != 0 {
		t.Fatalf("terminal accounting events=%d reservations=%d err=%v", events, reservations, err)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK), &snapshot)
	if len(snapshot.Records) != 1 || snapshot.Records[0].ID == f.recordID() || snapshot.Records[0].Name != "www-100.example.test" {
		t.Fatalf("replacement record was lost or old record remained: %+v", snapshot)
	}
}
