package cloudtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const fixtureVPCTag = "plumego-cmp-network"
const fixtureVPCReceipt = "fixture-vpc-receipt"

func TestAcceptancePublicCloudVPCCreation(t *testing.T) {
	testPublicCloudNetworkCreation(t, provider.Network)
}

func TestAcceptancePublicCloudSubnetCreation(t *testing.T) {
	testPublicCloudNetworkCreation(t, provider.Subnet)
}

func TestAcceptancePublicCloudSecurityGroupCreation(t *testing.T) {
	testPublicCloudNetworkCreation(t, provider.SecurityGroup)
}

func TestAcceptancePublicCloudSecurityRuleLifecycle(t *testing.T) {
	testPublicCloudNetworkCreation(t, provider.SecurityGroupRule)
}

func TestAcceptancePublicCloudElasticIPCreation(t *testing.T) {
	testPublicCloudNetworkCreation(t, provider.ElasticIP)
}

func testPublicCloudNetworkCreation(t *testing.T, kind provider.Kind) {
	t.Helper()
	for _, fixture := range []*vpcCreationFixture{
		{vendor: provider.TencentCVM, region: "ap-guangzhou", account: "100000000001", first: "vpc-first", second: "vpc-second"},
		{vendor: provider.AlibabaECS, region: "cn-hangzhou", account: "1000000000000002", first: "vpc-first", second: "vpc-second"},
		{vendor: provider.HuaweiECS, region: "cn-north-4", account: "1234567890abcdef1234567890abcdef",
			first: "20000000-0000-4000-8000-000000000010", second: "20000000-0000-4000-8000-000000000011"},
	} {
		t.Run(fixture.vendor, func(t *testing.T) {
			fixture.kind = kind
			switch kind {
			case provider.Subnet:
				fixture.useSubnet()
			case provider.SecurityGroup:
				fixture.useGroup()
			case provider.SecurityGroupRule:
				fixture.useRule()
			case provider.ElasticIP:
				fixture.useElasticIP()
			}
			ctx, db := t.Context(), dbtest.Open(t)
			if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO cmp_tenants VALUES ('tenant');
				INSERT INTO cmp_projects VALUES ('tenant','project');
				INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
					('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
				INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
					('tenant','project','operator','operator'),('tenant','project','reviewer','approver');
				INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash)
					VALUES ('fixture-vpc-events','tenant','project',decode(repeat('00',32),'hex'));`)
			if kind != provider.SecurityGroupRule && kind != provider.ElasticIP {
				exec("INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project',$1)", "create-"+string(kind))
			}
			const actor, reviewer = "fixture-vpc-operator-service-token", "fixture-vpc-reviewer-service-token"
			for _, role := range []struct{ principal, token string }{{"operator", actor}, {"reviewer", reviewer}} {
				digest := sha256.Sum256([]byte(role.token))
				exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
					VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, digest[:], role.principal)
			}
			const profileRef = "fixture/vpc"
			exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
				VALUES ('tenant',$1,'project',$1,$2,$3,'write_enabled')`, fixture.vendor, profileRef, fixture.remoteIdentity())
			if fixture.parent != "" {
				parentKind := provider.Network
				if kind == provider.SecurityGroupRule {
					parentKind = provider.SecurityGroup
				}
				exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
					VALUES ('tenant',$1,$2,$3,1,now())`, fixture.vendor, fixture.region, parentKind)
				exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
					SELECT scope_id,$2,'parent','available',1 FROM cmp_inventory_scopes WHERE account_id=$1 AND kind=$3`,
					fixture.vendor, fixture.parent, parentKind)
			}
			fixture.revoke = func(ctx context.Context) error {
				_, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET revision=revision+1
					WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')`)
				return err
			}
			fixture.token = func(ctx context.Context) (string, error) {
				var token string
				err := db.QueryRowContext(ctx, `SELECT n.launch_token FROM cmp_network_intents AS n
					JOIN cmp_instance_operations AS o USING (operation_id) WHERE o.status='applying' AND o.account_id=$1`, fixture.vendor).Scan(&token)
				return token, err
			}
			app, err := routers.NewAPI(db, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fixture.serve(t, w, r) }))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			save := func(name string, allow, allowRevocation bool) string {
				t.Helper()
				profile := cloud.PublicCloudProfile{Ref: profileRef, Config: publiccloud.Config{
					Regions: []string{fixture.region}, AccountID: fixture.account,
					AllowNetworkCreation:        allow && (kind == provider.Network || kind == provider.Subnet),
					AllowSecurityGroupCreation:  allow && kind == provider.SecurityGroup,
					AllowSecurityGroupRules:     allow && kind == provider.SecurityGroupRule,
					AllowSecurityRuleRevocation: allowRevocation && kind == provider.SecurityGroupRule,
					AllowElasticIPRelease:       allowRevocation && kind == provider.ElasticIP,
					AllowElasticIPBinding:       allowRevocation && kind == provider.ElasticIP,
				}}
				if kind == provider.ElasticIP && allow {
					profile.ElasticIPBandwidthLimit = fixtureEIPBandwidth
					profile.HuaweiElasticIPPool = fixture.addressPool
				}
				if fixture.vendor == provider.HuaweiECS {
					profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
				} else {
					profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
				}
				data, err := json.Marshal(profile)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			deny, allow := save("deny.json", false, false), save("allow.json", true, false)
			var revokeProfile string
			if kind == provider.SecurityGroupRule || kind == provider.ElasticIP {
				revokeProfile = save("removal.json", true, true)
			}
			var enabled, revocationEnabled atomic.Bool
			store := operations.NewStore(db)
			scope := identity.Scope{Tenant: "tenant", Project: "project"}
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() {
				done <- store.RunWorker(workerCtx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
					profile := deny
					if enabled.Load() {
						profile = allow
					}
					if revocationEnabled.Load() {
						profile = revokeProfile
					}
					return cloud.ConnectPublicCloudOperator(ctx, binding, profile, cloudFixtureTransport{endpoint, server.Client().Transport})
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
			submit := func(key, cidr string, want int) operations.Operation {
				t.Helper()
				input := operations.NetworkInput{Account: fixture.vendor, Region: fixture.region, Kind: kind,
					NetworkSpec: provider.NetworkSpec{Name: fixtureVPCName, CIDR: cidr, Zone: fixture.zone, ParentID: fixture.parent}}
				if kind == provider.SecurityGroupRule {
					rule := fixtureSecurityRule(cidr)
					input.Name, input.CIDR, input.Rule = "", "", &rule
				}
				if kind == provider.ElasticIP {
					input.BandwidthMbps, input.AddressPool = fixtureEIPBandwidth, fixture.addressPool
					switch key {
					case "invalid-bandwidth":
						input.BandwidthMbps = 4
					case "excess-bandwidth":
						input.BandwidthMbps++
					case "unapproved-pool":
						input.AddressPool = "5_sbgp"
					}
				}
				body, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+actor)
				r.Header.Set("Idempotency-Key", key)
				r.Header.Set("Content-Type", "application/json")
				if fixture.parent != "" {
					match := `"1"`
					if key == "stale-parent" {
						match = `"2"`
					}
					r.Header.Set("If-Match", match)
				}
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				var op operations.Operation
				if w.Code != want {
					t.Fatalf("create network status %d, want %d: %s", w.Code, want, w.Body)
				}
				if want == http.StatusAccepted {
					testsupport.DecodeProjectData(t, w, &op)
					if op.ID == "" || op.Status != "awaiting_approval" || op.ResourceKind != kind {
						t.Fatalf("invalid VPC receipt: %+v", op)
					}
				}
				return op
			}
			approve := func(op operations.Operation) {
				t.Helper()
				if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
					t.Fatal(err)
				}
			}
			wait := func(op operations.Operation, state string, advance bool) operations.Operation {
				t.Helper()
				return waitFixturePower(t, db, store, scope, actor, op.ID, state, advance)
			}
			firstCIDR, secondCIDR := fixtureVPCCIDR, fixtureVPCCIDR
			switch kind {
			case provider.Subnet:
				firstCIDR, secondCIDR = fixtureSubnetCIDR, fixtureSecondSubnetCIDR
			case provider.SecurityGroup:
				firstCIDR, secondCIDR = "", ""
				submit("invalid-group-cidr", fixtureVPCCIDR, http.StatusBadRequest)
			case provider.SecurityGroupRule:
				firstCIDR, secondCIDR = fixtureFirstRuleCIDR, fixtureSecondRuleCIDR
				submit("invalid-rule-cidr", "192.0.2.1/24", http.StatusBadRequest)
			case provider.ElasticIP:
				firstCIDR, secondCIDR = "", ""
				submit("invalid-bandwidth", firstCIDR, http.StatusBadRequest)
			default:
				submit("public-cidr", "203.0.113.0/24", http.StatusBadRequest)
			}
			if fixture.parent != "" {
				submit("stale-parent", firstCIDR, http.StatusPreconditionFailed)
			}
			disabled := submit("disabled", firstCIDR, http.StatusAccepted)
			approve(disabled)
			if failed := wait(disabled, "failed", false); failed.OutcomeReason != "provider_creation_blocked" || fixture.writes.Load() != 0 {
				t.Fatalf("default write guard: %+v", failed)
			}
			enabled.Store(true)
			if kind == provider.ElasticIP {
				blockedKeys := []string{"excess-bandwidth"}
				if fixture.vendor == provider.HuaweiECS {
					blockedKeys = append(blockedKeys, "unapproved-pool")
				}
				for _, key := range blockedKeys {
					blocked := submit(key, firstCIDR, http.StatusAccepted)
					approve(blocked)
					if failed := wait(blocked, "failed", false); failed.OutcomeReason != "provider_creation_blocked" || fixture.writes.Load() != 0 {
						t.Fatalf("elastic IP profile guard %s: %+v", key, failed)
					}
				}
			}
			if kind == provider.SecurityGroupRule {
				fixture.blockedGroup.Store(true)
				blocked := submit("blocked-parent", firstCIDR, http.StatusAccepted)
				approve(blocked)
				if failed := wait(blocked, "failed", false); failed.OutcomeReason != "provider_creation_blocked" || fixture.writes.Load() != 0 {
					t.Fatalf("unsupported group reached write: %+v", failed)
				}
				fixture.blockedGroup.Store(false)
			}
			if kind == provider.Subnet {
				outside := submit("outside-parent", "10.99.0.0/24", http.StatusAccepted)
				approve(outside)
				if failed := wait(outside, "failed", false); failed.OutcomeReason != "provider_creation_blocked" || fixture.writes.Load() != 0 {
					t.Fatalf("out-of-range CIDR reached create: %+v", failed)
				}
			}
			stale := submit("revoked-reviewer", firstCIDR, http.StatusAccepted)
			fixture.revokeOnIdentity.Store(true)
			approve(stale)
			if failed := wait(stale, "failed", false); failed.OutcomeReason != "dispatch_unavailable" || fixture.writes.Load() != 0 {
				t.Fatalf("stale approval allowed a write: %+v", failed)
			}
			first := submit("first", firstCIDR, http.StatusAccepted)
			if repeat := submit("first", firstCIDR, http.StatusAccepted); repeat.ID != first.ID || fixture.writes.Load() != 0 {
				t.Fatalf("idempotent VPC submission: %+v", repeat)
			}
			if kind != provider.Network {
				saved, err := store.Get(ctx, reviewer, scope, first.ID)
				name, cidr := fixtureVPCName, firstCIDR
				var parentRevision int64
				if fixture.parent != "" {
					parentRevision = 1
				}
				if kind == provider.SecurityGroupRule {
					name, cidr = "", ""
				}
				if err != nil || saved.NetworkIntent == nil || saved.NetworkIntent.Zone != fixture.zone ||
					saved.NetworkIntent.Name != name || saved.NetworkIntent.CIDR != cidr ||
					saved.NetworkIntent.ParentID != fixture.parent || saved.NetworkIntent.ParentRevision != parentRevision {
					t.Fatalf("approval intent lost network coordinates: %+v %v", saved.NetworkIntent, err)
				}
				if kind == provider.SecurityGroupRule &&
					(saved.NetworkIntent.Rule == nil || *saved.NetworkIntent.Rule != fixtureSecurityRule(firstCIDR)) {
					t.Fatalf("approval intent lost rule: %+v", saved.NetworkIntent)
				}
				if kind == provider.ElasticIP &&
					(saved.NetworkIntent.BandwidthMbps != fixtureEIPBandwidth || saved.NetworkIntent.AddressPool != fixture.addressPool) {
					t.Fatalf("approval intent lost elastic IP parameters: %+v", saved.NetworkIntent)
				}
			}
			if kind == provider.Subnet || kind == provider.SecurityGroupRule {
				submit("overlap", firstCIDR, http.StatusConflict)
			}
			approve(first)
			if observed := wait(first, "observing", false); observed.OutcomeReason != "submission_outcome_unknown" {
				t.Fatalf("unknown outcome: %+v", observed)
			}
			waitPendingCloudObservation(t, db, first.ID)
			if kind == provider.ElasticIP {
				exec(`WITH early_scope AS (
					INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,running,lease_until)
					VALUES ('tenant',$1,$2,'elastic-ip',1,false,now()) RETURNING scope_id
				)
				INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
					SELECT scope_id,$3,$4,'CREATING',1 FROM early_scope`, fixture.vendor, fixture.region, fixture.first, fixtureVPCName)
			}
			fixture.visible.Store(true)
			succeeded := wait(first, "succeeded", true)
			if !fixture.matchesCreatedID(succeeded.AssetID, fixture.first) || fixture.writes.Load() != 1 {
				t.Fatalf("loss recovery created a duplicate or wrong VPC: %+v", succeeded)
			}
			var owned, delivered bool
			if err := db.QueryRowContext(ctx, `SELECT
				EXISTS(SELECT 1 FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
					WHERE s.account_id=$1 AND s.kind=$4 AND o.external_id=$2 AND (
						($4='security-group-rule' AND o.links=jsonb_build_array(
							jsonb_build_object('kind','security-group','relation','in_security_group','id',$6::text)))
						OR EXISTS(SELECT 1 FROM cmp_asset_ownership AS a WHERE (a.scope_id,a.external_id)=(o.scope_id,o.external_id) AND a.project_id='project'))),
				EXISTS(SELECT 1 FROM cmp_operation_event_outbox AS d JOIN cmp_instance_operation_events AS e ON e.event_id=d.sequence
					WHERE d.event_type=$5 AND e.operation_id=$3)`, fixture.vendor, succeeded.AssetID, first.ID, kind, string(kind)+".operation.succeeded", fixture.parent).
				Scan(&owned, &delivered); err != nil || !owned || !delivered {
				t.Fatalf("missing atomic VPC result, owned %t, event %t, error %v", owned, delivered, err)
			}
			if kind == provider.ElasticIP {
				var stored bool
				err := db.QueryRowContext(ctx, `SELECT public_addresses=jsonb_build_array($3::text)
					AND status IN ('UNBIND','Available','DOWN')
					FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
					WHERE s.account_id=$1 AND s.kind='elastic-ip' AND o.external_id=$2`,
					fixture.vendor, succeeded.AssetID, fixtureAllocatedFirstIP).Scan(&stored)
				if err != nil || !stored {
					t.Fatalf("allocation confirmed without persisted address: %t %v", stored, err)
				}
			}
			if kind == provider.Subnet {
				var linked bool
				err := db.QueryRowContext(ctx, `SELECT o.links @> jsonb_build_array(
					jsonb_build_object('kind','network','relation','in_network','id',$3::text),
					jsonb_build_object('kind','zone','relation','in_zone','id',$4::text))
					FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
					WHERE s.account_id=$1 AND s.kind='subnet' AND o.external_id=$2`, fixture.vendor, fixture.first, fixture.parent, fixture.zone).Scan(&linked)
				if err != nil || !linked {
					t.Fatalf("subnet topology missing: %t %v", linked, err)
				}
			}
			fixture.badEvidence.Store(true)
			second := submit("second", secondCIDR, http.StatusAccepted)
			approve(second)
			if review := wait(second, "needs_review", true); review.ProviderRequestID != fixtureVPCReceipt ||
				review.OutcomeReason != "creation_requires_evidence" || review.AssetID != "" || fixture.writes.Load() != 2 {
				t.Fatalf("accepted wrong network evidence: %+v", review)
			}
			fixture.badEvidence.Store(false)
			if _, err := store.RetryCreationObservation(ctx, reviewer, scope, second.ID,
				operations.ObservationRetry{EvidenceRef: "fixture/network-evidence-confirmed"}); err != nil {
				t.Fatal(err)
			}
			recovered := wait(second, "succeeded", true)
			if !fixture.matchesCreatedID(recovered.AssetID, fixture.second) ||
				(kind == provider.SecurityGroupRule && recovered.AssetID == succeeded.AssetID) || fixture.writes.Load() != 2 {
				t.Fatalf("read-only recovery did not resolve resource: %+v", recovered)
			}
			if kind == provider.SecurityGroupRule {
				fixture.exerciseRuleRevocation(t, db, store, app, scope, actor, reviewer, recovered.AssetID, &revocationEnabled)
				submit("recreate-revoked-rule", secondCIDR, http.StatusAccepted)
			}
			if kind == provider.ElasticIP {
				if !t.Run("binding", func(t *testing.T) {
					fixture.exerciseElasticIPBinding(t, db, store, app, scope, actor, reviewer, &revocationEnabled)
				}) {
					t.FailNow()
				}
				t.Run("release", func(t *testing.T) {
					fixture.exerciseElasticIPRelease(t, db, store, app, scope, actor, reviewer, &revocationEnabled)
				})
			}
		})
	}
}

const fixtureVPCName, fixtureVPCCIDR = "private-network", "10.0.0.0/16"

type vpcCreationFixture struct {
	vendor, region, account, first, second string
	kind                                   provider.Kind
	parent, zone, addressPool              string
	revoke                                 func(context.Context) error
	token                                  func(context.Context) (string, error)
	revokeOnIdentity, visible, badEvidence atomic.Bool
	blockedGroup                           atomic.Bool
	invalidAbsence, holdRulePoint          atomic.Bool
	duplicateRule                          atomic.Bool
	writes, revokes                        atomic.Int32
	releaseMode                            atomic.Bool
	binding                                *elasticIPBindingFixture
	protectedIP                            atomic.Bool
	releases                               atomic.Int32
	marker                                 atomic.Pointer[string]
}

func (f *vpcCreationFixture) remoteIdentity() string {
	if f.vendor == provider.HuaweiECS {
		return f.account + ":" + fixtureHuaweiProject
	}
	return f.account
}

func (f *vpcCreationFixture) remember(t *testing.T, r *http.Request, marker, token string) bool {
	t.Helper()
	original, err := f.token(r.Context())
	if err != nil {
		t.Error(err)
		return false
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{fixtureVPCTag, f.vendor, f.remoteIdentity(), f.region, original}, "\x00")))
	if marker != fmt.Sprintf("%x", digest) ||
		((f.vendor == provider.AlibabaECS || (f.vendor == provider.TencentCVM && f.kind == provider.ElasticIP)) && token != original) {
		return false
	}
	f.marker.Store(&marker)
	f.writes.Add(1)
	return true
}

func (f *vpcCreationFixture) replayMode(t *testing.T, r *http.Request) bool {
	t.Helper()
	if f.revokeOnIdentity.CompareAndSwap(true, false) {
		if err := f.revoke(r.Context()); err != nil {
			t.Error(err)
			return false
		}
	}
	return true
}

func (f *vpcCreationFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	valid := false
	switch f.vendor {
	case provider.TencentCVM:
		valid = f.tencent(t, w, r)
	case provider.AlibabaECS:
		valid = f.alibaba(t, w, r)
	case provider.HuaweiECS:
		valid = f.huawei(t, w, r)
	}
	if !valid {
		t.Errorf("unexpected VPC request from %s for %s", f.vendor, r.Host)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *vpcCreationFixture) coordinates() (string, string) {
	id, cidr := f.first, fixtureVPCCIDR
	if f.writes.Load() > 1 {
		id = f.second
	}
	if f.badEvidence.Load() {
		cidr = "10.1.0.0/16"
	}
	return id, cidr
}

func (f *vpcCreationFixture) tencent(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Method != http.MethodPost || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	send := func(value any) { testsupport.WriteJSON(t, w, map[string]any{"Response": value}) }
	action := r.Header.Get("X-TC-Action")
	if action == "GetCallerIdentity" && r.Host == "sts.tencentcloudapi.com" {
		if f.parent == "" && !f.replayMode(t, r) {
			return false
		}
		send(map[string]string{"AccountId": f.account})
		return true
	}
	if f.binding != nil && f.binding.running.Load() {
		return f.tencentElasticIPBinding(t, w, r, body, action, send)
	}
	if f.kind == provider.Subnet {
		return f.tencentSubnet(t, w, r, body, action, send)
	}
	if r.Host != "vpc.tencentcloudapi.com" || r.Header.Get("X-TC-Version") != "2017-03-12" {
		return false
	}
	if f.kind == provider.SecurityGroup {
		return f.tencentGroup(t, w, r, body, action, send)
	}
	if f.kind == provider.SecurityGroupRule {
		return f.tencentRule(t, w, r, body, action, send)
	}
	if f.kind == provider.ElasticIP {
		if f.releaseMode.Load() {
			return f.tencentElasticIPRelease(w, body, action, send)
		}
		return f.tencentElasticIP(t, w, r, body, action, send)
	}
	switch action {
	case "CreateVpc":
		var payload struct {
			VpcName, CidrBlock string
			Tags               []struct{ Key, Value string }
		}
		if json.Unmarshal(body, &payload) != nil || payload.VpcName != fixtureVPCName || payload.CidrBlock != fixtureVPCCIDR ||
			len(payload.Tags) != 1 || payload.Tags[0].Key != fixtureVPCTag || !f.remember(t, r, payload.Tags[0].Value, "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]any{"RequestId": fixtureVPCReceipt, "Vpc": map[string]string{"VpcId": f.second}})
		}
	case "DescribeVpcs":
		var payload struct {
			Limit   string
			VpcIds  []string
			Filters []struct {
				Name   string
				Values []string
			}
		}
		marker := f.marker.Load()
		if marker == nil || json.Unmarshal(body, &payload) != nil || payload.Limit != "2" {
			return false
		}
		if f.writes.Load() == 1 {
			if len(payload.VpcIds) != 0 || len(payload.Filters) != 1 || payload.Filters[0].Name != "tag:"+fixtureVPCTag ||
				!slices.Equal(payload.Filters[0].Values, []string{*marker}) {
				return false
			}
		} else if !slices.Equal(payload.VpcIds, []string{f.second}) || len(payload.Filters) != 0 {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			id, cidr := f.coordinates()
			rows = append(rows, map[string]any{"VpcId": id, "VpcName": fixtureVPCName, "CidrBlock": cidr,
				"TagSet": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}})
		}
		send(map[string]any{"VpcSet": rows, "TotalCount": len(rows)})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method != http.MethodPost || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	send := func(value any) { testsupport.WriteJSON(t, w, value) }
	action, q := r.Header.Get("X-ACS-Action"), r.URL.Query()
	if action == "GetCallerIdentity" && r.Host == "sts.aliyuncs.com" {
		if f.parent == "" && !f.replayMode(t, r) {
			return false
		}
		send(map[string]string{"AccountId": f.account})
		return true
	}
	if f.binding != nil && f.binding.running.Load() {
		return f.alibabaElasticIPBinding(t, w, r, action, send)
	}
	if f.kind == provider.SecurityGroup && action != "DescribeVpcs" {
		return f.alibabaGroup(t, w, r, action, send)
	}
	if f.kind == provider.SecurityGroupRule {
		return f.alibabaRule(t, w, r, action, send)
	}
	if f.kind == provider.ElasticIP {
		if f.releaseMode.Load() {
			return f.alibabaElasticIPRelease(w, r, action, send)
		}
		return f.alibabaElasticIP(t, w, r, action, send)
	}
	if r.Host != "vpc.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2016-04-28" || q.Get("RegionId") != f.region {
		return false
	}
	if f.kind == provider.Subnet || f.kind == provider.SecurityGroup {
		return f.alibabaSubnet(t, r, action, w, send)
	}
	switch action {
	case "CreateVpc":
		if q.Get("VpcName") != fixtureVPCName || q.Get("CidrBlock") != fixtureVPCCIDR || q.Get("EnableIpv6") != "false" ||
			q.Get("Tag.1.Key") != fixtureVPCTag || !f.remember(t, r, q.Get("Tag.1.Value"), q.Get("ClientToken")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]string{"RequestId": fixtureVPCReceipt, "VpcId": f.second})
		}
	case "DescribeVpcs":
		marker := f.marker.Load()
		if marker == nil || q.Get("VpcOwnerId") != f.account || q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" {
			return false
		}
		if f.writes.Load() == 1 {
			if q.Has("VpcId") || q.Get("Tag.1.Key") != fixtureVPCTag || q.Get("Tag.1.Value") != *marker {
				return false
			}
		} else if q.Get("VpcId") != f.second || q.Has("Tag.1.Key") {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			id, cidr := f.coordinates()
			rows = append(rows, map[string]any{"VpcId": id, "VpcName": fixtureVPCName, "CidrBlock": cidr,
				"RegionId": f.region, "OwnerId": json.Number(f.account), "Status": "Available",
				"Tags": map[string]any{"Tag": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}}})
		}
		send(map[string]any{"Vpcs": map[string]any{"Vpc": rows}, "TotalCount": len(rows), "PageNumber": 1, "PageSize": 10})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huawei(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	send := func(value any) { testsupport.WriteJSON(t, w, value) }
	if r.Method == http.MethodGet && r.Host == "iam.myhuaweicloud.com" && r.URL.Path == "/v3/auth/tokens" &&
		r.Header.Get("X-Subject-Token") == fixtureHuaweiIAM {
		if f.parent == "" && !f.replayMode(t, r) {
			return false
		}
		send(map[string]any{"token": map[string]any{"expires_at": time.Now().UTC().Add(time.Hour),
			"project": map[string]any{"id": fixtureHuaweiProject, "domain": map[string]string{"id": f.account}}}})
		return true
	}
	if f.binding != nil && f.binding.running.Load() {
		return f.huaweiElasticIPBinding(t, w, r, send)
	}
	if r.Host != "vpc."+f.region+".myhuaweicloud.com" {
		return false
	}
	if f.kind == provider.Subnet {
		return f.huaweiSubnet(t, r, w, send)
	}
	if f.kind == provider.SecurityGroup {
		return f.huaweiGroup(t, w, r, send)
	}
	if f.kind == provider.SecurityGroupRule {
		return f.huaweiRule(t, w, r, send)
	}
	if f.kind == provider.ElasticIP {
		if f.releaseMode.Load() {
			return f.huaweiElasticIPRelease(w, r, send)
		}
		return f.huaweiElasticIP(t, w, r, send)
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/vpcs":
		var payload struct {
			VPC struct{ Name, CIDR, Description string }
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&payload) != nil || r.Header.Get("Content-Type") != "application/json" ||
			payload.VPC.Name != fixtureVPCName || payload.VPC.CIDR != fixtureVPCCIDR ||
			!strings.HasPrefix(payload.VPC.Description, fixtureVPCTag+":") ||
			!f.remember(t, r, strings.TrimPrefix(payload.VPC.Description, fixtureVPCTag+":"), "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"request_id": fixtureVPCReceipt, "vpc": map[string]string{"id": f.second, "tenant_id": fixtureHuaweiProject}})
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v3/"+fixtureHuaweiProject+"/vpc/vpcs":
		q, marker := r.URL.Query(), f.marker.Load()
		if marker == nil || q.Get("limit") != "2" {
			return false
		}
		description := fixtureVPCTag + ":" + *marker
		if f.writes.Load() == 1 {
			if q.Has("id") || q.Get("description") != description {
				return false
			}
		} else if q.Get("id") != f.second || q.Has("description") {
			return false
		}
		rows := []map[string]string{}
		if f.visible.Load() {
			id, cidr := f.coordinates()
			rows = append(rows, map[string]string{"id": id, "name": fixtureVPCName, "cidr": cidr,
				"description": description, "status": "ACTIVE", "project_id": fixtureHuaweiProject})
		}
		send(map[string]any{"vpcs": rows, "page_info": map[string]int{"current_count": len(rows)}})
	default:
		return false
	}
	return true
}
