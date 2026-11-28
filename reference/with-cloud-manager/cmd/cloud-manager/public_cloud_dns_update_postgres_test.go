package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudDNSUpdate(t *testing.T) {
	for _, base := range dnsStatusFixtures() {
		t.Run(base.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDNSUpdate(t, &dnsUpdateFixture{dnsStatusFixture: base})
		})
	}
}

func testPublicCloudDNSUpdate(t *testing.T, f *dnsUpdateFixture) {
	t.Helper()
	const actor, reviewer = "fixture-public-dns-update-operator", "fixture-public-dns-update-reviewer"
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	cfg := publicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		cfg.ProjectID, cfg.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		cfg.AccessKeyID, cfg.AccessKeySecret, cfg.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveUpdate(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	run := func(args ...string) {
		t.Helper()
		if err := runPublicCloud(ctx, db, args, f.vendor, path, transport); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func() { run("refresh-"+f.command+"-dns-zone", "tenant", f.vendor, f.id) }
	run("scan-"+f.command, "tenant", f.vendor, "global", "dns-zone")
	refresh()
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	store := operations.NewStore(db)
	var allowWrite atomic.Bool
	startDNSUpdateFixtureWorker(t, store, cfg.Config, transport, &allowWrite)

	const root = "/v1/tenants/tenant/projects/"
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	recordsPath := root + "project/assets/dns-zones/" + f.id + "/records?account=" + f.vendor + "&offset=100"
	read := func() inventory.DNSRecordsPage {
		t.Helper()
		var snapshot inventory.DNSRecordsPage
		decodeProjectData(t, scheduleResponse(t, app, http.MethodGet, recordsPath, actor, "", "", http.StatusOK), &snapshot)
		if len(snapshot.Records) != 1 {
			t.Fatalf("unexpected DNS snapshot: %+v", snapshot)
		}
		return snapshot
	}
	submit := func(project, cmd, key, version string, payload any, status int) operations.Operation {
		t.Helper()
		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, root+project+"/dns-zones/"+cmd, bytes.NewReader(content))
		req.Header.Set("Authorization", "Bearer "+actor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", version)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: got HTTP %d instead of %d: %s", key, w.Code, status, w.Body)
		}
		var op operations.Operation
		if status == http.StatusAccepted {
			decodeProjectData(t, w, &op)
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, status, true)
	}
	snapshot := read()
	update := operations.DNSRecordUpdateInput{Account: f.vendor, AssetID: f.id, RecordID: f.recordID(),
		ObservedAt: snapshot.ObservedAt, Values: []string{"192.0.2.44"}, TTL: 300}
	if f.vendor == provider.HuaweiECS {
		update.Values = append(update.Values, "192.0.2.43")
	}
	send := func(key string, payload operations.DNSRecordUpdateInput, status int) operations.Operation {
		t.Helper()
		return submit("project", "record-update", key, `"1"`, payload, status)
	}
	send("stale-snapshot", withDNSUpdateObservation(update, snapshot.ObservedAt.Add(-time.Second)), http.StatusPreconditionFailed)
	submit("project", "record-update", "stale-owner", `"2"`, update, http.StatusPreconditionFailed)
	submit("other", "record-update", "wrong-project", `"1"`, update, http.StatusForbidden)
	invalid := update
	invalid.Values = []string{"2001:db8::1"}
	send("wrong-address-family", invalid, http.StatusBadRequest)
	invalid.Values = []string{"192.0.2.44", "192.0.2.44"}
	send("duplicated-address", invalid, http.StatusBadRequest)
	invalid.Values, invalid.TTL = snapshot.Records[0].Values, snapshot.Records[0].TTL
	send("no-change", invalid, http.StatusBadRequest)

	disabled := send("disabled", update, http.StatusAccepted)
	if _, err := store.Approve(ctx, actor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval returned %v", err)
	}
	approve(disabled)
	if done := wait(disabled, "failed"); done.OutcomeReason != "dns_record_update_disabled" {
		t.Fatalf("unexpected disabled result: %+v", done)
	}
	allowWrite.Store(true)
	f.drift.Store(true)
	stale := send("remote-drift", update, http.StatusAccepted)
	approve(stale)
	if done := wait(stale, "failed"); done.OutcomeReason != "dns_record_update_preconditions_unconfirmed" || f.updateWrites.Load() != 0 {
		t.Fatalf("stale remote record was updated: %+v", done)
	}
	f.drift.Store(false)
	if f.vendor == provider.HuaweiECS {
		f.healthCheck.Store(true)
		managed := send("health-checked", update, http.StatusAccepted)
		approve(managed)
		wait(managed, "failed")
		f.healthCheck.Store(false)
	}

	var output bytes.Buffer
	content, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	if err := runOperationsClient(ctx, []string{"update-dns-record", "tenant", "project", "--idempotency-key", "begin", "--if-match", "1"},
		server.URL, actor, bytes.NewReader(content), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil || op.ID == "" || op.Action != "update-dns-record" {
		t.Fatalf("invalid CLI receipt: %s %v", output.String(), err)
	}
	if f.vendor == provider.HuaweiECS {
		update.Values = []string{"192.0.2.43", "192.0.2.44"}
	}
	if replay := send("begin", update, http.StatusAccepted); replay.ID != op.ID || op.Status != "awaiting_approval" {
		t.Fatalf("wrong idempotent receipt: %+v %+v", op, replay)
	}
	toDisable := false
	submit("project", "record-status", "competing", `"1"`, operations.DNSRecordStatusInput{
		Account: f.vendor, AssetID: f.id, RecordID: f.recordID(), ObservedAt: snapshot.ObservedAt, Enabled: &toDisable,
	}, http.StatusConflict)
	approve(op)
	done := wait(op, "succeeded")
	intent := done.DNSRecordUpdateIntent
	if f.updateWrites.Load() != 1 || intent == nil || intent.Record.TTL != 60 || intent.TTL != update.TTL ||
		!slices.Equal(intent.Values, update.Values) || (f.vendor == provider.HuaweiECS && !f.seenPending.Load()) {
		t.Fatalf("invalid confirmed update: %+v writes=%d", done, f.updateWrites.Load())
	}
	scheduleResponse(t, app, http.MethodGet, recordsPath, actor, "", "", http.StatusNotFound)
	refresh()
	snapshot = read()
	if snapshot.Records[0].TTL != update.TTL || !slices.Equal(snapshot.Records[0].Values, update.Values) {
		t.Fatalf("refreshed snapshot missed update: %+v", snapshot)
	}

	update.ObservedAt, update.TTL = snapshot.ObservedAt, 600
	f.loseReceipt.Store(true)
	unknown := send("lost-receipt", update, http.StatusAccepted)
	approve(unknown)
	wait(unknown, "needs_review")
	if _, err := store.Resolve(ctx, reviewer, scope, unknown.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/asserted"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed native evidence: %v", err)
	}
	allowWrite.Store(false)
	f.drift.Store(false)
	retryNameChangeObservation(t, store, scope, reviewer, unknown.ID)
	wait(unknown, "succeeded")
	if f.updateWrites.Load() != 2 {
		t.Fatalf("observation re-sent the write: %d", f.updateWrites.Load())
	}
	var events, reservations int
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='dns-zone.operation.succeeded'),
		(SELECT count(*) FROM cmp_dns_zone_change_reservations)`).Scan(&events, &reservations)
	if err != nil || events != 2 || reservations != 0 {
		t.Fatalf("terminal accounting: events=%d reservations=%d error=%v", events, reservations, err)
	}
}

func withDNSUpdateObservation(input operations.DNSRecordUpdateInput, observedAt time.Time) operations.DNSRecordUpdateInput {
	input.ObservedAt = observedAt
	return input
}

func startDNSUpdateFixtureWorker(t *testing.T, store operations.Store, config publiccloud.Config, transport http.RoundTripper, allow *atomic.Bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			profile := config
			profile.AllowDNSRecordUpdate = allow.Load()
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
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("DNS update worker did not stop")
		}
	})
}
