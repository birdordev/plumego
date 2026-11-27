package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePriorityInstanceRetirement(t *testing.T) {
	for _, f := range []*priorityRetirementFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, region: "cn-hangzhou", account: "1000000000000002"},
			instance: "i-retirefixture", disk: "d-rootfixture", zone: "cn-hangzhou-a"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, region: "cn-north-4", account: "1234567890abcdef1234567890abcdef"},
			instance: "90eea036-a806-474b-956e-485343e1a891", disk: "287b09ee-2373-4fea-a43c-d3ed9acd9c43", zone: "cn-north-4a"},
	} {
		t.Run(f.vendor, func(t *testing.T) {
			t.Parallel()
			testPriorityInstanceRetirement(t, f)
		})
	}
}

func testPriorityInstanceRetirement(t *testing.T, f *priorityRetirementFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: f.disk, id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), retirementRequester, retirementReviewer)
	retirementFixtureExec(t, db, `UPDATE cmp_memberships SET role='admin' WHERE principal_id='operator'`)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveRetirement(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	path, command := priorityRetirementProfile(t, f)
	for _, kind := range []string{"instance", "volume"} {
		if err := runPublicCloud(ctx, db, []string{command, "tenant", f.vendor, f.region, kind}, f.vendor, path, transport); err != nil {
			t.Fatalf("scan %s: %v", kind, err)
		}
	}
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return connectPublicCloudOperator(ctx, binding, path, transport)
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
			t.Error("retirement worker did not stop")
		}
	})

	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	args := []string{"preview-retire", "tenant", "project", "--account", f.vendor, "--region", f.region, "--asset-id", f.instance}
	if err := runOperationsClient(ctx, args, server.URL, retirementRequester, nil, &out, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var preview operations.InstanceRetirementPreview
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.SystemDiskID != f.disk || preview.OwnershipRevision != 1 {
		t.Fatalf("preview did not resolve the reverse attachment from inventory: %s %v", out.String(), err)
	}
	if _, err := store.PreviewInstanceRetirement(ctx, retirementRequester, identity.Scope{Tenant: "tenant", Project: "other"},
		f.vendor, f.region, f.instance); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("other project could read a retirement target: %v", err)
	}

	input := operations.InstanceRetirementInput{Account: f.vendor, Region: f.region, AssetID: f.instance,
		SystemDiskID: preview.SystemDiskID, ConfirmID: f.instance}
	submit := func(key string) operations.Operation {
		t.Helper()
		op, err := store.SubmitInstanceRetirement(ctx, retirementRequester, scope, key, preview.OwnershipRevision, input)
		if err != nil || op.Status != "awaiting_approval" {
			t.Fatalf("submit retirement %s: %+v %v", key, op, err)
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, retirementReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	f.unsafeDisk.Store(true)
	invalid := submit("unsafe-retirement")
	approve(invalid)
	waitFixturePower(t, db, store, scope, retirementRequester, invalid.ID, "failed", true)
	if f.writes.Load() != 0 {
		t.Fatal("unsafe disk policy allowed a write")
	}
	f.unsafeDisk.Store(false)
	op := submit("safe-retirement")
	if replay := submit("safe-retirement"); replay.ID != op.ID {
		t.Fatal("replay queued another retirement")
	}
	if f.writes.Load() != 0 {
		t.Fatal("unapproved retirement reached the provider")
	}
	approve(op)
	waitRetirementRound(t, db, op.ID, 1, 0)
	waitRetirementRound(t, db, op.ID, 2, 0)
	f.diskAbsent.Store(true)
	waitRetirementRound(t, db, op.ID, 3, 1)
	f.limited.Store(true)
	waitRetirementRound(t, db, op.ID, 4, 0)
	f.limited.Store(false)
	f.badEvidence.Store(true)
	review := waitFixturePower(t, db, store, scope, retirementRequester, op.ID, "needs_review", true)
	if review.ProviderRequestID != priorityRetirementReceipt || f.writes.Load() != 1 {
		t.Fatal("invalid absence evidence lost the receipt or repeated dispatch")
	}
	f.badEvidence.Store(false)
	retryRetirementObservation(t, store, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/approved-protocol-recheck"})
	waitRetirementRound(t, db, op.ID, 1, 1)
	result := waitFixturePower(t, db, store, scope, retirementRequester, op.ID, "succeeded", true)
	if result.ProviderRequestID != priorityRetirementReceipt || f.writes.Load() != 1 {
		t.Fatalf("receipt or single-dispatch guarantee violated: %s writes=%d", result.ProviderRequestID, f.writes.Load())
	}
	var deleted bool
	var reservations, delivered int
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT deletion_confirmed_at IS NOT NULL FROM cmp_inventory_objects WHERE external_id=$1),
		(SELECT count(*) FROM cmp_instance_retirement_reservations WHERE operation_id=$2),
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS e ON e.event_id=box.sequence
			WHERE e.operation_id=$2 AND box.event_type='instance.operation.succeeded')`, f.instance, op.ID).Scan(&deleted, &reservations, &delivered)
	if err != nil || !deleted || reservations != 0 || delivered != 1 {
		t.Fatalf("retirement was not published atomically: deleted=%t reservations=%d delivered=%d error=%v", deleted, reservations, delivered, err)
	}
}

func priorityRetirementProfile(t *testing.T, f *priorityRetirementFixture) (string, string) {
	t.Helper()
	profile := publicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{
		AccountID: f.account, Regions: []string{f.region}, AllowInstanceRetirement: true,
	}}
	command := "scan-alibaba"
	if f.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
		command = "scan-huawei"
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, command
}

func retirementFixtureExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
