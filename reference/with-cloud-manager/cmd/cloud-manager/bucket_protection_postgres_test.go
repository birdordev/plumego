package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/inventory"
)

const bucketProtectionFixturePath = "/v1/tenants/lab/projects/compute/governance/bucket-protection"

func TestAcceptanceBucketProtectionReport(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	execFreshness(t, db, `
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,running)
			SELECT tenant_id,account_id,'global','object-bucket',1,now(),false FROM cmp_cloud_accounts;
		WITH fixture(tenant,account,name,location,missing) AS (VALUES
			('lab','ali','alpha-oss','cn-hangzhou',NULL::bigint),
			('lab','hw','beta-obs','cn-north-4',NULL),
			('lab','paused','paused-cos','ap-beijing',NULL),
			('lab','tc','old-cos','ap-guangzhou',2),
			('lab','tc','owned-cos','ap-guangzhou',NULL),
			('lab','tc','deleted-cos','ap-guangzhou',NULL),
			('foreign','tc','alpha-oss','cn-hangzhou',NULL))
		INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,missing_generation,removed_at,object_bucket)
			SELECT s.scope_id,f.name||'@2026-01-01T00:00:00Z',f.name,'observed',1,f.missing,
				CASE WHEN f.name='deleted-cos' THEN now() END,jsonb_build_object('location',f.location)
			FROM fixture f JOIN cmp_inventory_scopes s ON (s.tenant_id,s.account_id)=(f.tenant,f.account)
			WHERE s.kind='object-bucket';
		INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
			SELECT s.tenant_id,s.scope_id,o.external_id,'compute',2 FROM cmp_inventory_objects o
			JOIN cmp_inventory_scopes s USING (scope_id) WHERE o.name='owned-cos';
		INSERT INTO cmp_bucket_encryption(scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
			SELECT scope_id,external_id,1,object_bucket->>'location',
				CASE name WHEN 'alpha-oss' THEN 'unconfigured' WHEN 'beta-obs' THEN 'unconfigured' ELSE 'aes256' END,now(),
				CASE WHEN name='beta-obs' THEN now()-interval '2 days' ELSE now() END
			FROM cmp_inventory_objects WHERE name IN ('alpha-oss','beta-obs','paused-cos','old-cos');
		INSERT INTO cmp_bucket_versioning(scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
			SELECT scope_id,external_id,1,object_bucket->>'location',
				CASE name WHEN 'beta-obs' THEN 'suspended' WHEN 'alpha-oss' THEN 'enabled' ELSE 'disabled' END,now(),
				CASE WHEN name='alpha-oss' THEN now()-interval '2 days' ELSE now() END
			FROM cmp_inventory_objects WHERE name IN ('alpha-oss','beta-obs','paused-cos','old-cos','owned-cos')`)

	read := func(path string) inventory.BucketProtectionReport {
		t.Helper()
		response := freshnessRequest(t, app, path, freshnessToken)
		requireFreshnessStatus(t, response, http.StatusOK)
		var report inventory.BucketProtectionReport
		decodeProjectData(t, response, &report)
		if strings.Contains(response.Body.String(), "fixture/private") || report.Items == nil {
			t.Fatal("invalid bucket protection response shape")
		}
		return report
	}
	const path = bucketProtectionFixturePath
	want := inventory.BucketProtectionSummary{
		Total: 5, InventoryUnavailable: 2,
		Encryption: inventory.BucketProtectionCount{Fresh: 1, Stale: 1, Unobserved: 3, Flagged: 1},
		Versioning: inventory.BucketProtectionCount{Fresh: 2, Stale: 1, Unobserved: 2, Flagged: 2},
	}
	var names []string
	for next := ""; ; {
		params := url.Values{"limit": {"1"}}
		if next != "" {
			params.Set("after", next)
		}
		report := read(path + "?" + params.Encode())
		if report.Summary != want || report.Window != 86400 || report.AsOf.IsZero() || len(report.Items) != 1 {
			t.Fatalf("unexpected bucket protection page: %+v", report)
		}
		name := report.Items[0].Name
		if slices.Contains(names, name) {
			t.Fatalf("bucket repeated across pages: %s", name)
		}
		names = append(names, name)
		next = report.Next
		if next == "" {
			break
		}
	}
	if !slices.Equal(names, []string{"alpha-oss", "beta-obs", "paused-cos", "old-cos", "owned-cos"}) {
		t.Fatalf("wrong bucket ordering or visibility: %v", names)
	}
	flagged := read(path + "?finding=encryption_unconfigured")
	if flagged.Summary != want || len(flagged.Items) != 1 || flagged.Items[0].Name != "alpha-oss" ||
		flagged.Items[0].Encryption.State != "unconfigured" || flagged.Items[0].Versioning.Status != "stale" {
		t.Fatalf("stale observation was treated as current configuration: %+v", flagged)
	}
	region := read(path + "?provider=huawei-ecs&location=cn-north-4&window_seconds=259200")
	if region.Summary.Total != 1 || region.Summary.Encryption.Flagged != 1 || len(region.Items) != 1 {
		t.Fatalf("window or location filters failed: %+v", region)
	}
	unavailable := read(path + "?finding=inventory_unavailable")
	for _, item := range unavailable.Items {
		if item.InventoryAvailable || item.Encryption.State != "" || item.Versioning.ObservedAt != nil {
			t.Fatalf("old observations leaked for unavailable inventory: %+v", item)
		}
	}
	if len(unavailable.Items) != 2 {
		t.Fatalf("missing unavailable buckets: %+v", unavailable)
	}
	shared := strings.Replace(path, "/compute/", "/shared/", 1)
	if report := read(shared); report.Summary.Total != 1 || len(report.Items) != 1 || report.Items[0].Name != "old-cos" {
		t.Fatalf("shared project read an explicitly owned bucket: %+v", report)
	}
	execFreshness(t, db, `UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','ali');
		UPDATE cmp_inventory_scopes SET credential_revision=2 WHERE (tenant_id,account_id,kind)=('lab','ali','object-bucket');
		DELETE FROM cmp_cloud_account_grants WHERE (tenant_id,account_id,project_id)=('lab','tc','shared')`)
	if report := read(shared); report.Summary.Total != 0 {
		t.Fatalf("revoked grant remained visible: %+v", report)
	}
	if report := read(path + "?account=ali"); len(report.Items) != 1 || !report.Items[0].InventoryAvailable ||
		report.Items[0].Encryption.Status != "unobserved" || report.Items[0].Encryption.State != "" {
		t.Fatalf("credential rotation carried forward an old observation: %+v", report)
	}
	checkBucketProtectionCLIAndSpec(t, app)
}

func checkBucketProtectionCLIAndSpec(t *testing.T, app http.Handler) {
	t.Helper()
	const path = bucketProtectionFixturePath
	requireFreshnessStatus(t, freshnessRequest(t, app, path, ""), http.StatusUnauthorized)
	for _, target := range []string{strings.Replace(path, "/compute/", "/denied/", 1), strings.Replace(path, "/lab/", "/foreign/", 1)} {
		requireFreshnessStatus(t, freshnessRequest(t, app, target, freshnessToken), http.StatusForbidden)
	}
	for _, query := range []string{"?limit=101", "?location=cn%2Ftest", "?window_seconds=59", "?provider=aws-ec2",
		"?finding=bogus", "?account=ali&account=tc", "?after=invalid", "?account=ali&after=tc/owned-cos@2026-01-01T00:00:00Z"} {
		requireFreshnessStatus(t, freshnessRequest(t, app, path+query, freshnessToken), http.StatusBadRequest)
	}
	spec := freshnessRequest(t, app, "/openapi.json", "")
	requireFreshnessStatus(t, spec, http.StatusOK)
	var doc openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc.Paths["/v1/tenants/{tenant}/projects/{project}/governance/bucket-protection"].Get
	if operation == nil || operation.OperationID != "listBucketProtection" || len(operation.Security) != 2 {
		t.Fatalf("missing bucket protection API contract: %+v", operation)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := runGovernanceClient(t.Context(), []string{"bucket-protection", "lab", "compute", "--provider", "huawei-ecs",
		"--location", "cn-north-4", "--finding", "versioning_inactive", "--limit", "1"},
		server.URL, freshnessToken, nil, &output, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	var report inventory.BucketProtectionReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || report.Summary.Total != 1 ||
		len(report.Items) != 1 || report.Items[0].Name != "beta-obs" {
		t.Fatalf("CLI report differs from HTTP: %+v / %v", report, err)
	}
}
