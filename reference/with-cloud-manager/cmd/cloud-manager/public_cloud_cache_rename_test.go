package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

const cacheRenameBase = "/v1/tenants/tenant/projects/"

func TestAcceptancePublicCloudCacheRename(t *testing.T) {
	for _, target := range []struct{ vendor, account, region, product, id string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", provider.TencentRedis, "crs-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-beijing", provider.AlibabaTair, "r-fixture"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", provider.HuaweiDCS, "70000000-0000-4000-8000-000000000019"},
	} {
		t.Run(target.id, func(t *testing.T) {
			t.Parallel()
			f := &cacheRenameFixture{cacheFixture: cacheFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				id:                 target.id, product: target.product,
			}}
			testPublicCloudCacheRename(t, f)
		})
	}
}

func testPublicCloudCacheRename(t *testing.T, f *cacheRenameFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedCacheRename(t, db, f)
	f.name.Store("original-name")
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveRename(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	store, assets := operations.NewStore(db), inventory.NewStore(db)
	var enabled atomic.Bool
	startCacheRenameWorker(t, store, f, transport, &enabled)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	payload := func(current, desired string) []byte {
		t.Helper()
		content, err := json.Marshal(operations.CacheRenameInput{
			Account: f.vendor, Region: f.region, AssetID: f.id, CurrentName: &current, Name: desired,
		})
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	submit := func(project, key, revision string, body []byte, want int) operations.Operation {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, cacheRenameBase+project+"/caches/rename", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+cacheRenameActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("rename %s status=%d want=%d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.ResourceKind != provider.CacheInstance || op.Action != "rename-cache-instance" ||
				w.Header().Get("Location") != cacheRenameBase+project+"/operations/"+op.ID {
				t.Fatalf("invalid receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, cacheRenameReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, cacheRenameActor, op.ID, state, true)
	}

	body := payload("original-name", "renamed-cache")
	submit("other", "wrong-project", `"1"`, body, http.StatusForbidden)
	submit("project", "wrong-revision", `"2"`, body, http.StatusPreconditionFailed)
	submit("project", "wrong-name", `"1"`, payload("expired", "Next_name"), http.StatusPreconditionFailed)
	submit("project", "invalid-name", `"1"`, payload("original-name", "1invalid"), http.StatusBadRequest)

	disabled := submit("project", "disabled", `"1"`, body, http.StatusAccepted)
	if disabled.Status != "awaiting_approval" {
		t.Fatal(disabled.Status)
	}
	if _, err := store.Approve(ctx, cacheRenameActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester could approve: %v", err)
	}
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "cache_rename_disabled" || f.writes.Load() != 0 {
		t.Fatalf("disabled action unexpectedly sent: %+v", op)
	}

	enabled.Store(true)
	drift := submit("project", "remote-drift", `"1"`, body, http.StatusAccepted)
	f.name.Store("externally_changed")
	approve(drift)
	if op := wait(drift, "failed"); op.OutcomeReason != "cache_rename_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("drift did not stop dispatch: %+v", op)
	}
	f.name.Store("original-name")

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"rename-cache-instance", "tenant", "project", "--idempotency-key", "first", "--if-match", "1"},
		local.URL, cacheRenameActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var first operations.Operation
	if err := json.Unmarshal(output.Bytes(), &first); err != nil || first.Status != "awaiting_approval" || first.ID == "" {
		t.Fatalf("incorrect CLI receipt: %s %v", &output, err)
	}
	if replay := submit("project", "first", `"1"`, body, http.StatusAccepted); replay.ID != first.ID {
		t.Fatalf("replay changed operation ID: %+v", replay)
	}
	submit("project", "first", `"1"`, payload("original-name", "Other_name"), http.StatusConflict)
	submit("project", "parallel", `"1"`, body, http.StatusConflict)
	transfer := inventory.OwnershipTransfer{Account: f.vendor, Region: f.region, Kind: provider.CacheInstance, AssetID: f.id, TargetProject: "other"}
	if _, err := assets.TransferOwnership(ctx, cacheRenameActor, scope, 1, transfer); !errors.Is(err, identity.ErrAccountConflict) {
		t.Fatalf("active operation did not reserve ownership: %v", err)
	}
	claimed, err := assets.Claim(ctx, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.CacheInstance}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approve(first)
	first = wait(first, "succeeded")
	if first.ProviderRequestID != fixtureCacheRenameReceipt || first.CacheRenameIntent == nil ||
		first.CacheRenameIntent.Product != f.product || first.CacheRenameIntent.CurrentName != "original-name" ||
		first.CacheRenameIntent.Name != "renamed-cache" || first.CacheRenameIntent.OwnershipRevision != 1 {
		t.Fatalf("wrong intent or receipt: %+v", first)
	}
	if _, _, err := assets.ScanPage(ctx, claimed, staleAssetDiscovery{item: provider.Object{ID: f.id, Name: "original-name"}}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("older inventory scan could publish: %v", err)
	}

	f.dropReply.Store(true)
	second := submit("project", "lost-reply", `"1"`, payload("renamed-cache", "recovered-name"), http.StatusAccepted)
	approve(second)
	second = wait(second, "needs_review")
	if second.ProviderRequestID != "" || f.writes.Load() != 2 {
		t.Fatalf("lost receipt caused another dispatch: %+v, writes=%d", second, f.writes.Load())
	}
	if _, err := store.Resolve(ctx, cacheRenameReviewer, scope, second.ID, operations.Resolution{
		Decision: "succeeded", EvidenceRef: "fixture/verified-instance",
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed vendor observation: %v", err)
	}
	if _, err := assets.TransferOwnership(ctx, cacheRenameActor, scope, 1, transfer); !errors.Is(err, identity.ErrAccountConflict) {
		t.Fatalf("unresolved operation did not retain reservation: %v", err)
	}
	f.wrongTarget.Store(false)
	enabled.Store(false)
	retryNameChangeObservation(t, store, scope, cacheRenameReviewer, second.ID)
	wait(second, "succeeded")
	if f.writes.Load() != 2 {
		t.Fatalf("observation recovery repeated the mutation: %d", f.writes.Load())
	}
	assertCacheRenamePublished(t, db, f, second.ID)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, cacheRenameBase+"project/operations?kind=cache-instance&limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+cacheRenameActor)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cache operation list: %d %s", w.Code, w.Body)
	}
	var list operations.OperationPage
	decodeProjectData(t, w, &list)
	if len(list.Items) != 4 {
		t.Fatalf("cache filter did not return the four submissions: %+v", list)
	}
}
