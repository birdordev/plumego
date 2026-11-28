package cloudtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func testInstanceResizeOptions(t *testing.T, db *sql.DB, server *httptest.Server, f *instanceResizeFixture,
	connect func(context.Context, identity.CloudAccountBinding) (*publiccloud.Client, error),
) {
	t.Helper()
	ctx, assets := t.Context(), inventory.NewStore(db)
	arguments := []string{"tenant", "project", "--account", f.vendor, "--region", f.region, "--asset-id", f.id}
	call := func(command string) inventory.ResizeOptions {
		t.Helper()
		var output bytes.Buffer
		if err := commands.RunOperationsClient(ctx, append([]string{command}, arguments...), server.URL, instanceResizeActor,
			bytes.NewReader(nil), &output, server.Client().Transport); err != nil {
			t.Fatal(err)
		}
		var options inventory.ResizeOptions
		if err := json.Unmarshal(output.Bytes(), &options); err != nil {
			t.Fatal(err)
		}
		return options
	}

	unauthorized := server.URL + "/v1/tenants/tenant/projects/other/instances/resize-options?" +
		url.Values{"account": {f.vendor}, "region": {f.region}, "asset_id": {f.id}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, unauthorized, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+instanceResizeActor)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-project preview status %d", response.StatusCode)
	}

	if got := call("resize-instance-options"); got.State != "unavailable" || len(got.Types) != 0 {
		t.Fatalf("unexpected initial snapshot: %+v", got)
	}
	for range 2 {
		if got := call("refresh-instance-resize-options"); got.State != "pending" || got.CurrentType != f.original || got.OwnershipRevision != 1 {
			t.Fatalf("unsupported refresh: %+v", got)
		}
	}
	var reads atomic.Int32
	reader := func(ctx context.Context, binding identity.CloudAccountBinding, scope provider.Scope, id, current string) (provider.InstanceResizeOptions, error) {
		reads.Add(1)
		reader, err := connect(ctx, binding)
		if err != nil {
			return provider.InstanceResizeOptions{}, err
		}
		return reader.InstanceResizeOptions(ctx, scope, id, current)
	}
	if worked, err := assets.CollectInstanceResizeOptions(ctx, reader); !worked || err != nil {
		t.Fatalf("preview did not execute: work=%v error=%v", worked, err)
	}
	for _, command := range []string{"resize-instance-options", "refresh-instance-resize-options"} {
		if got := call(command); got.State != "ready" || !slices.Equal(got.Types, []string{f.target}) ||
			got.Zone == "" || got.ObservedAt == nil || got.ExpiresAt == nil {
			t.Fatalf("invalid confirmed preview: %+v", got)
		}
	}
	if worked, err := assets.CollectInstanceResizeOptions(ctx, reader); worked || err != nil || reads.Load() != 1 || f.sends.Load() != 0 {
		t.Fatalf("fresh result triggered remote work: work=%v error=%v reads=%d sends=%d", worked, err, reads.Load(), f.sends.Load())
	}

	if _, err := db.ExecContext(ctx, `UPDATE cmp_instance_resize_options SET expires_at=statement_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if got := call("resize-instance-options"); len(got.Types) != 0 || got.State != "unavailable" {
		t.Fatalf("expired options were visible: %+v", got)
	}
	if got := call("refresh-instance-resize-options"); got.State != "pending" {
		t.Fatalf("expired options were not refreshed: %+v", got)
	}
	rotate := func(ctx context.Context, binding identity.CloudAccountBinding, scope provider.Scope, id, current string) (provider.InstanceResizeOptions, error) {
		options, err := reader(ctx, binding, scope, id, current)
		if err != nil {
			return options, err
		}
		_, err = db.ExecContext(ctx, `UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1 WHERE account_id=$1`, f.vendor)
		return options, err
	}
	if worked, err := assets.CollectInstanceResizeOptions(ctx, rotate); !worked || err != nil {
		t.Fatalf("rotated preview did not settle: work=%v error=%v", worked, err)
	}
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM cmp_instance_resize_options`).Scan(&state); err != nil || state != "unavailable" {
		t.Fatalf("rotated evidence was retained: state=%s error=%v", state, err)
	}
	client, err := connect(ctx, identity.CloudAccountBinding{Provider: f.vendor, RemoteIdentity: f.remoteIdentity(), Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Instance}
	claim, err := assets.ClaimWithCredentialRevision(ctx, key, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := assets.ScanPage(ctx, claim, client); err != nil || !complete {
		t.Fatalf("scan after rotation incomplete: complete=%v error=%v", complete, err)
	}
	if got := call("resize-instance-options"); got.State != "unavailable" || len(got.Types) != 0 {
		t.Fatalf("stale collector published across rotation: %+v", got)
	}
}
