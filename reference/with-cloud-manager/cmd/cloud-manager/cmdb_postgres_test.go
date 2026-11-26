package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"with-cloud-manager/internal/cmdb"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const cmdbFixtureToken = "fixture-cmdb-source-project-viewer-token"

type cmdbFixtureTransport func(*http.Request) (*http.Response, error)

func (f cmdbFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func seedCMDBSource(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := migrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	cmdbExec(t, db, `INSERT INTO cmp_tenants VALUES ('tenant'),('outside');
		INSERT INTO cmp_projects VALUES ('tenant','home'),('tenant','shared'),('outside','home');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		VALUES ('tenant','mirror','service','fixture','mirror');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
		VALUES ('tenant','home','mirror','viewer'),('tenant','shared','mirror','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','account','home','tencent-cvm','local/ref','tencent:fixture','read_only'),
		 ('outside','account','home','alibaba-ecs','private/ref','alibaba:outside','read_only');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id)
		VALUES ('tenant','account','shared');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','account','ap-guangzhou','instance',1,now()),
		 ('tenant','account','ap-guangzhou','network',1,now()),
		 ('outside','account','ap-guangzhou','instance',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links,public_addresses)
		SELECT scope_id,'i-one','原云主机','running',1,'[{"kind":"network","id":"vpc-one","relation":"in_network"}]','["192.0.2.99"]'
		FROM cmp_inventory_scopes WHERE tenant_id='tenant' AND kind='instance';
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'vpc-one','共享项目网络','available',1 FROM cmp_inventory_scopes WHERE kind='network'
		UNION ALL SELECT scope_id,'i-one','外租户云主机','running',1 FROM cmp_inventory_scopes WHERE tenant_id='outside';
		INSERT INTO cmp_asset_ownership (tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,'vpc-one','shared',2 FROM cmp_inventory_scopes WHERE kind='network';`)
	digest := sha256.Sum256([]byte(cmdbFixtureToken))
	cmdbExec(t, db, `INSERT INTO cmp_service_tokens (tenant_id,principal_id,token_hash,expires_at)
		VALUES ('tenant','mirror',$1,clock_timestamp()+interval '10 minutes')`, digest[:])
}

func cmdbExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptanceCMDBProjectMirror(t *testing.T) {
	source, target := dbtest.Open(t), dbtest.Open(t)
	seedCMDBSource(t, source)
	app, err := newAPI(source, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewTLSServer(app)
	defer api.Close()
	transport := api.Client().Transport
	spec, err := api.Client().Get(api.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	document, readErr := io.ReadAll(spec.Body)
	closeErr := spec.Body.Close()
	if readErr != nil || closeErr != nil || spec.StatusCode != http.StatusOK ||
		!bytes.Contains(document, []byte(`"/v1/tenants/{tenant}/projects/{project}/governance/cmdb-snapshot"`)) ||
		!bytes.Contains(document, []byte(cmdb.MediaType)) {
		t.Fatalf("strict OpenAPI did not expose the CMDB stream: %d %v", spec.StatusCode, errors.Join(readErr, closeErr))
	}
	if err := runCMDBCommand(t.Context(), target, []string{"cmdb-migrate"}, "", "", io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	run := func(command, tenant, project string, roundTripper http.RoundTripper) (cmdb.Receipt, error) {
		var output bytes.Buffer
		err := runCMDBCommand(t.Context(), target, []string{command, tenant, project}, api.URL, cmdbFixtureToken, &output, roundTripper)
		if err != nil {
			return cmdb.Receipt{}, err
		}
		var receipt cmdb.Receipt
		return receipt, json.Unmarshal(output.Bytes(), &receipt)
	}
	syncProject := func(project string) cmdb.Receipt {
		t.Helper()
		result, err := run("cmdb-sync", "tenant", project, transport)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	readAsset := func(project, id string) inventory.CMDBAsset {
		t.Helper()
		var body []byte
		if err := target.QueryRowContext(t.Context(), `SELECT asset FROM cmp_cmdb_assets
			WHERE (source_url,tenant_id,project_id,asset_id)=($1,'tenant',$2,$3)`, api.URL, project, id).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte("192.0.2.99")) || bytes.Contains(body, []byte("private/ref")) {
			t.Fatalf("CMDB exposed data outside the asset field contract: %s", body)
		}
		var asset inventory.CMDBAsset
		if err := json.Unmarshal(body, &asset); err != nil {
			t.Fatal(err)
		}
		return asset
	}

	first := syncProject("home")
	shared := syncProject("shared")
	if first.Count != 1 || shared.Count != 2 || first.Upserted != 1 || first.Removed != 0 {
		t.Fatalf("unexpected scoped snapshots: %+v %+v", first, shared)
	}
	if asset := readAsset("home", "i-one"); len(asset.Links) != 0 || asset.Name != "原云主机" {
		t.Fatalf("project saw unowned topology or tenant data: %+v", asset)
	}
	if asset := readAsset("shared", "i-one"); len(asset.Links) != 1 || asset.Links[0].ID != "vpc-one" {
		t.Fatalf("shared project lost visible topology: %+v", asset)
	}
	if again := syncProject("home"); again.Upserted != 0 || again.Removed != 0 || again.Generation <= first.Generation {
		t.Fatalf("unchanged inventory did not reconcile idempotently: %+v", again)
	}
	if _, err := run("cmdb-sync", "outside", "home", transport); !isCMDBStatus(err, http.StatusForbidden) {
		t.Fatalf("cross-tenant CMDB export accepted: %v", err)
	}
	attempts := 0
	redirect := cmdbFixtureTransport(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"/ignored"}}, Body: http.NoBody}, nil
	})
	if _, err := run("cmdb-sync", "tenant", "home", redirect); !isCMDBStatus(err, http.StatusTemporaryRedirect) || attempts != 1 {
		t.Fatalf("receiver followed a CMDB redirect: %d %v", attempts, err)
	}

	cmdbExec(t, source, `UPDATE cmp_inventory_objects SET missing_generation=2 WHERE external_id='i-one'
		AND scope_id IN (SELECT scope_id FROM cmp_inventory_scopes WHERE tenant_id='tenant');
		UPDATE cmp_asset_ownership SET project_id='home',revision=3 WHERE external_id='vpc-one';
		DELETE FROM cmp_cloud_account_grants WHERE tenant_id='tenant' AND project_id='shared';`)
	if updated := syncProject("home"); updated.Count != 2 || updated.Upserted != 2 || updated.Removed != 0 {
		t.Fatalf("pending absence or transferred asset lost: %+v", updated)
	}
	if asset := readAsset("home", "i-one"); asset.State != "missing_candidate" || len(asset.Links) != 1 {
		t.Fatalf("pending absence or newly visible relation lost: %+v", asset)
	}
	if revoked := syncProject("shared"); revoked.Count != 0 || revoked.Removed != 2 {
		t.Fatalf("revoked project retained stale rows: %+v", revoked)
	}

	cmdbExec(t, source, `UPDATE cmp_inventory_objects SET removed_at=statement_timestamp() WHERE external_id='i-one'
		AND scope_id IN (SELECT scope_id FROM cmp_inventory_scopes WHERE tenant_id='tenant')`)
	truncated := cmdbFixtureTransport(func(r *http.Request) (*http.Response, error) {
		reply, body, err := captureCMDBResponse(transport, r)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, errors.New("fixture returned an empty snapshot")
		}
		end := bytes.LastIndexByte(body[:len(body)-1], '\n') + 1
		return attachCMDBBody(reply, body[:end]), nil
	})
	if _, err := run("cmdb-sync", "tenant", "home", truncated); !errors.Is(err, cmdb.ErrInvalidSnapshot) {
		t.Fatalf("missing trailer applied a partial snapshot: %v", err)
	}
	if asset := readAsset("home", "i-one"); asset.State != "missing_candidate" {
		t.Fatalf("failed sync damaged previous publication: %+v", asset)
	}
	cmdbExec(t, target, `ALTER TABLE cmp_cmdb_reconciliations ADD CONSTRAINT fixture_receipt_failure CHECK (generation<1) NOT VALID`)
	if _, err := run("cmdb-sync", "tenant", "home", transport); err == nil {
		t.Fatal("publication succeeded when receipt transaction failed")
	}
	readAsset("home", "i-one")
	cmdbExec(t, target, "ALTER TABLE cmp_cmdb_reconciliations DROP CONSTRAINT fixture_receipt_failure")
	if removed := syncProject("home"); removed.Count != 1 || removed.Removed != 1 {
		t.Fatalf("confirmed remove did not reconcile after retry: %+v", removed)
	}
	var duplicate bytes.Buffer
	builder := cmdb.NewSnapshotWriter(&duplicate, cmdb.Project{Tenant: "tenant", Project: "home"})
	item := readAsset("home", "vpc-one")
	if err := errors.Join(builder.Start(time.Now()), builder.Asset(item), builder.Asset(item), builder.Finish()); err != nil {
		t.Fatal(err)
	}
	duplicateTransport := cmdbFixtureTransport(func(*http.Request) (*http.Response, error) {
		return attachCMDBBody(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {cmdb.MediaType}}}, duplicate.Bytes()), nil
	})
	if _, err := run("cmdb-sync", "tenant", "home", duplicateTransport); !errors.Is(err, cmdb.ErrInvalidSnapshot) {
		t.Fatalf("synchronized a validly hashed snapshot with duplicate assets: %v", err)
	}

	oldSnapshot, release := make(chan []byte, 1), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	oldTransport := cmdbFixtureTransport(func(r *http.Request) (*http.Response, error) {
		reply, body, err := captureCMDBResponse(transport, r)
		if err != nil {
			return nil, err
		}
		oldSnapshot <- body
		select {
		case <-release:
			return attachCMDBBody(reply, body), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	oldErr := make(chan error, 1)
	go func() {
		_, err := run("cmdb-sync", "tenant", "home", oldTransport)
		oldErr <- err
	}()
	var previous []byte
	select {
	case previous = <-oldSnapshot:
	case err := <-oldErr:
		t.Fatalf("could not capture old snapshot: %v", err)
	case <-t.Context().Done():
		t.Fatal("old snapshot request timed out")
	}
	cmdbExec(t, source, "UPDATE cmp_inventory_objects SET name='更新网络' WHERE external_id='vpc-one'")
	latest := syncProject("home")
	close(release)
	if err := <-oldErr; !errors.Is(err, cmdb.ErrStaleSnapshot) || readAsset("home", "vpc-one").Name != "更新网络" {
		t.Fatalf("older import overwrote newer publication: %v", err)
	}
	replayOld := cmdbFixtureTransport(func(*http.Request) (*http.Response, error) {
		return attachCMDBBody(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {cmdb.MediaType}}}, previous), nil
	})
	if _, err := run("cmdb-sync", "tenant", "home", replayOld); !errors.Is(err, cmdb.ErrStaleSnapshot) {
		t.Fatalf("old source snapshot accepted at new local generation: %v", err)
	}
	if status, err := run("cmdb-status", "tenant", "home", nil); err != nil || status.Generation != latest.Generation {
		t.Fatalf("status lost latest committed receipt: %+v %v", status, err)
	}
}

func isCMDBStatus(err error, status int) bool {
	var reported cmdb.HTTPStatusError
	return errors.As(err, &reported) && reported.Status == status
}

func captureCMDBResponse(transport http.RoundTripper, req *http.Request) (*http.Response, []byte, error) {
	reply, err := transport.RoundTrip(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = reply.Body.Close() }()
	body, err := io.ReadAll(reply.Body)
	return reply, body, err
}

func attachCMDBBody(reply *http.Response, body []byte) *http.Response {
	reply.Body = io.NopCloser(bytes.NewReader(body))
	reply.ContentLength = int64(len(body))
	return reply
}

func TestAcceptanceCMDBConsistentPages(t *testing.T) {
	db := dbtest.Open(t)
	seedCMDBSource(t, db)
	cmdbExec(t, db, `INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'i-page-'||lpad(n::text,4,'0'),'原始页','running',1
		FROM cmp_inventory_scopes CROSS JOIN generate_series(1,300) AS numbers(n)
		WHERE tenant_id='tenant' AND kind='instance';`)
	store := inventory.NewStore(db)
	seen := 0
	scope := identity.Scope{Tenant: "tenant", Project: "home"}
	markCapture := func(time.Time) error { return nil }
	err := store.StreamCMDB(t.Context(), cmdbFixtureToken, scope, markCapture, func(asset inventory.CMDBAsset) error {
		seen++
		if seen == 256 {
			cmdbExec(t, db, "UPDATE cmp_inventory_objects SET name='盘点后改名' WHERE external_id='i-page-0300'")
		}
		if asset.ID == "i-page-0300" && asset.Name != "原始页" {
			t.Errorf("cursor crossed a source snapshot boundary: %+v", asset)
		}
		return nil
	})
	if err != nil || seen != 301 {
		t.Fatalf("inconsistent pages: records=%d error=%v", seen, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = store.StreamCMDB(ctx, cmdbFixtureToken, scope, markCapture, func(asset inventory.CMDBAsset) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot ignored caller cancellation: %v", err)
	}
	reassigned := false
	err = store.StreamCMDB(t.Context(), cmdbFixtureToken, scope, markCapture, func(inventory.CMDBAsset) error {
		if !reassigned {
			cmdbExec(t, db, `UPDATE cmp_memberships SET active=false WHERE project_id='home' AND principal_id='mirror';
				UPDATE cmp_memberships SET active=true WHERE project_id='home' AND principal_id='mirror'`)
			reassigned = true
		}
		return nil
	})
	if !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("snapshot completed after membership changed during streaming: %v", err)
	}
}
