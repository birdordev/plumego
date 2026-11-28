package cloudtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const fixtureLaunchTag = "plumego-cmp-launch"

func TestAcceptancePublicCloudInstanceCreation(t *testing.T) {
	for _, tc := range []struct {
		name, region, account, subnet, size, instance string
	}{
		{provider.TencentCVM, "ap-guangzhou", "100000000001", "subnet-fixture", "S5.MEDIUM2", "ins-fixture"},
		{provider.AlibabaECS, "cn-hangzhou", "1000000000000002", "vsw-fixture", "ecs.g7.large", "i-fixture"},
		{provider.HuaweiECS, "cn-north-4", "1234567890abcdef1234567890abcdef", fixtureHuaweiNetwork, "s3.large.1", fixtureHuaweiFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
				INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
					('tenant','project','operator','operator'),('tenant','project','reviewer','approver');
				INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create');
				INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash)
					VALUES ('fixture-sink','tenant','project',decode(repeat('00',32),'hex'));`)
			const actor, reviewer = "public-cloud-launch-operator-token", "public-cloud-launch-reviewer-token"
			for _, role := range []struct{ principal, token string }{{"operator", actor}, {"reviewer", reviewer}} {
				digest := sha256.Sum256([]byte(role.token))
				exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
					VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, digest[:], role.principal)
			}

			scope := identity.Scope{Tenant: "tenant", Project: "project"}
			input := operations.CreateInput{Account: tc.name, Region: tc.region, InstanceSpec: provider.InstanceSpec{
				Name: "Private service", Size: tc.size, ImageID: "img-fixture", NetworkID: tc.subnet, SecurityGroup: "sg-fixture",
			}}
			remoteIdentity, vpc := tc.account, "vpc-fixture"
			if tc.name == provider.HuaweiECS {
				input.Name, input.ImageID, input.SecurityGroup = "private-service", fixtureHuaweiImage, fixtureHuaweiGroup
				remoteIdentity, vpc = tc.account+":"+fixtureHuaweiProject, fixtureHuaweiVPC
			}
			fixture := &publicCloudCreationFixture{
				provider: tc.name, region: tc.region, account: tc.account, instance: tc.instance, spec: input.InstanceSpec,
				revoke: func(ctx context.Context) error {
					_, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET revision=revision+1
						WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')`)
					return err
				},
				persistedToken: func(ctx context.Context) (string, error) {
					var token string
					err := db.QueryRowContext(ctx, `SELECT l.launch_token FROM cmp_instance_launches AS l
						JOIN cmp_instance_operations AS o USING(operation_id) WHERE o.account_id=$1 AND o.status='applying'`, tc.name).Scan(&token)
					return token, err
				},
			}
			const ref = "fixture/launch"
			exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
				VALUES ('tenant',$1,'project',$1,$2,$3,'write_enabled')`, tc.name, ref, remoteIdentity)
			exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
				SELECT 'tenant',$1,$2,kind,1,now() FROM (VALUES ('image'),('subnet'),('security-group')) AS v(kind)`, tc.name, tc.region)
			imageState, subnetState := "Available", "Available"
			if tc.name == provider.TencentCVM {
				imageState, subnetState = "NORMAL", "observed"
			}
			exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
				SELECT s.scope_id,d.asset,d.asset,d.state,1,d.links::jsonb
				FROM cmp_inventory_scopes AS s JOIN (VALUES
					('image',$2::text,$5::text,'[]'),('subnet',$3,$6,$7::text),
					('security-group',$4,'observed',$8::text)) AS d(kind,asset,state,links) USING(kind)
				WHERE s.account_id=$1`, tc.name, input.ImageID, input.NetworkID, input.SecurityGroup, imageState, subnetState,
				fmt.Sprintf(`[{"kind":"network","id":%q,"relation":"in_network"}]`, vpc), fixture.groupLinks())
			exec("INSERT INTO cmp_instance_offerings VALUES ('tenant',$1,$2,$3)", tc.name, tc.region, tc.size)
			app, err := routers.NewAPI(db, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture.serve(t, w, r)
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := cloudFixtureTransport{endpoint, server.Client().Transport}
			dir := t.TempDir()
			save := func(filename string, sizes []string) string {
				t.Helper()
				profile := cloud.PublicCloudProfile{Ref: ref, Config: publiccloud.Config{
					Regions: []string{tc.region}, AccountID: tc.account, InstanceCreateSizes: sizes,
				}}
				if tc.name == provider.HuaweiECS {
					profile.ProjectID, profile.IAMToken, profile.HuaweiRootVolumeType = fixtureHuaweiProject, fixtureHuaweiIAM, "SSD"
				} else {
					profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
				}
				data, err := json.Marshal(profile)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, filename)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			deny, allow := save("deny.json", nil), save("allow.json", []string{tc.size})
			var enabled atomic.Bool
			store := operations.NewStore(db)
			worker, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() {
				done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
					path := deny
					if enabled.Load() {
						path = allow
					}
					return cloud.ConnectPublicCloudOperator(ctx, binding, path, transport)
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
			submit := func(key string, in operations.CreateInput, code int) operations.Operation {
				t.Helper()
				data, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/instances", bytes.NewReader(data))
				r.Header.Set("Authorization", "Bearer "+actor)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != code {
					t.Fatalf("create status %d, want %d: %s", w.Code, code, w.Body)
				}
				var operation operations.Operation
				if code == http.StatusAccepted {
					testsupport.DecodeProjectData(t, w, &operation)
					if operation.ID == "" || operation.Status != "awaiting_approval" {
						t.Fatalf("invalid approval receipt: %+v", operation)
					}
				}
				return operation
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
			wrong := input
			wrong.NetworkID += "-foreign"
			submit("foreign", wrong, http.StatusForbidden)
			disabled := submit("disabled", input, http.StatusAccepted)
			approve(disabled)
			if failed := wait(disabled, "failed", false); failed.OutcomeReason != "provider_creation_blocked" || fixture.writes.Load() != 0 {
				t.Fatalf("default creation guard: %+v", failed)
			}
			enabled.Store(true)
			if tc.name != provider.AlibabaECS {
				stale := submit("stale-reviewer", input, http.StatusAccepted)
				fixture.revokeOnRead.Store(true)
				approve(stale)
				if failed := wait(stale, "failed", false); failed.OutcomeReason != "dispatch_unavailable" || fixture.writes.Load() != 0 {
					t.Fatalf("stale approval allowed a write: %+v", failed)
				}
			}
			launch := submit("launch", input, http.StatusAccepted)
			if replay := submit("launch", input, http.StatusAccepted); replay.ID != launch.ID || fixture.writes.Load() != 0 {
				t.Fatalf("idempotent, unapproved launch: %+v", replay)
			}
			approve(launch)
			if observing := wait(launch, "observing", false); observing.OutcomeReason != "submission_outcome_unknown" {
				t.Fatalf("lost create response did not enter observation: %+v", observing)
			}
			waitPendingCloudObservation(t, db, launch.ID)
			fixture.ready.Store(true)
			if succeeded := wait(launch, "succeeded", true); succeeded.AssetID != tc.instance || fixture.writes.Load() != 1 {
				t.Fatalf("creation did not reconcile exactly once: %+v", succeeded)
			}
			var published, instanceLinked bool
			if err := db.QueryRowContext(ctx, `SELECT
				EXISTS(SELECT 1 FROM cmp_operation_event_outbox AS d JOIN cmp_instance_operation_events AS e ON e.event_id=d.sequence
					WHERE d.event_type='instance.operation.succeeded' AND e.operation_id=$1
					AND strpos(convert_from(d.body,'UTF8'),$5)=0),
				o.links @> jsonb_build_array(jsonb_build_object('kind','subnet','id',$3::text,'relation','in_subnet'))
				FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
				WHERE (s.account_id,s.kind,o.external_id)=($2,'instance',$4)`, launch.ID, tc.name, tc.subnet, tc.instance, *fixture.marker.Load()).
				Scan(&published, &instanceLinked); err != nil || !instanceLinked || !published {
				t.Fatalf("asset linkage %t, event %t, error %v", instanceLinked, published, err)
			}
			fixture.wrongResult.Store(true)
			fixture.jobPending.Store(tc.name == provider.HuaweiECS)
			mismatch := submit("mismatched-resource", input, http.StatusAccepted)
			approve(mismatch)
			if tc.name == provider.HuaweiECS {
				wait(mismatch, "observing", false)
				waitPendingCloudObservation(t, db, mismatch.ID)
				fixture.jobPending.Store(false)
			}
			if review := wait(mismatch, "needs_review", true); review.OutcomeReason != "creation_requires_evidence" ||
				review.ProviderRequestID != "fixture-launch-receipt" || fixture.writes.Load() != 2 {
				t.Fatalf("remote mismatch accepted: %+v", review)
			}
			fixture.wrongResult.Store(false)
			if _, err := store.RetryCreationObservation(ctx, reviewer, scope, mismatch.ID, operations.ObservationRetry{
				EvidenceRef: "fixture/remote-evidence-verified",
			}); err != nil {
				t.Fatal(err)
			}
			if succeeded := wait(mismatch, "succeeded", true); succeeded.AssetID != fixture.currentInstance() || fixture.writes.Load() != 2 {
				t.Fatalf("read-only recovery changed write count or target: %+v", succeeded)
			}
		})
	}
}

const (
	fixtureLaunchKey     = "fixture-launch-key"
	fixtureLaunchSecret  = "fixture-launch-secret"
	fixtureLaunchSession = "fixture-launch-session"
)

type publicCloudCreationFixture struct {
	provider, region, account, instance string
	spec                                provider.InstanceSpec
	revoke                              func(context.Context) error
	persistedToken                      func(context.Context) (string, error)
	revokeOnRead, ready, wrongResult    atomic.Bool
	jobPending                          atomic.Bool
	writes                              atomic.Int32
	marker                              atomic.Pointer[string]
}

func (f *publicCloudCreationFixture) groupLinks() string {
	if f.provider != provider.AlibabaECS {
		return "[]"
	}
	return `[{"kind":"network","id":"vpc-fixture","relation":"in_network"}]`
}

func (f *publicCloudCreationFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	valid := false
	switch f.provider {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		valid = err == nil && f.tencent(t, w, r, body)
	case provider.AlibabaECS:
		valid = f.alibaba(t, w, r)
	case provider.HuaweiECS:
		valid = f.huawei(t, w, r)
	}
	if !valid {
		t.Errorf("unexpected launch protocol for %s host %s", f.provider, r.Host)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *publicCloudCreationFixture) tencent(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte) bool {
	t.Helper()
	if r.Method != http.MethodPost || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != fixtureLaunchSession {
		return false
	}
	send := func(value any) { testsupport.WriteJSON(t, w, map[string]any{"Response": value}) }
	action := r.Header.Get("X-TC-Action")
	if action == "GetCallerIdentity" && r.Host == "sts.tencentcloudapi.com" {
		send(map[string]string{"AccountId": f.account})
		return true
	}
	if r.Header.Get("X-TC-Version") != "2017-03-12" {
		return false
	}
	switch {
	case r.Host == "vpc.tencentcloudapi.com" && action == "DescribeSubnets":
		var query struct{ SubnetIds []string }
		if json.Unmarshal(body, &query) != nil || !slices.Equal(query.SubnetIds, []string{f.spec.NetworkID}) {
			return false
		}
		if f.revokeOnRead.CompareAndSwap(true, false) {
			if err := f.revoke(r.Context()); err != nil {
				t.Error(err)
				return false
			}
		}
		send(map[string]any{"TotalCount": 1, "SubnetSet": []map[string]string{
			{"SubnetId": f.spec.NetworkID, "VpcId": "vpc-fixture", "Zone": f.region + "-3"},
		}})
		return true
	case r.Host == "cvm.tencentcloudapi.com" && action == "RunInstances":
		var request struct {
			InstanceName, ImageId, InstanceType, InstanceChargeType, ClientToken string
			InstanceCount                                                        int
			Placement                                                            struct{ Zone string }
			VirtualPrivateCloud                                                  struct{ VpcId, SubnetId string }
			SecurityGroupIds                                                     []string
			InternetAccessible                                                   struct {
				PublicIpAssigned        *bool
				InternetMaxBandwidthOut *int
			}
			LoginSettings    map[string]string
			TagSpecification []struct {
				ResourceType string
				Tags         []struct{ Key, Value string }
			}
		}
		if json.Unmarshal(body, &request) != nil || request.InstanceName != f.spec.Name || request.ImageId != f.spec.ImageID ||
			request.InstanceType != f.spec.Size || request.InstanceChargeType != "POSTPAID_BY_HOUR" || request.InstanceCount != 1 ||
			request.Placement.Zone != f.region+"-3" || request.VirtualPrivateCloud.VpcId != "vpc-fixture" ||
			request.VirtualPrivateCloud.SubnetId != f.spec.NetworkID || !slices.Equal(request.SecurityGroupIds, []string{f.spec.SecurityGroup}) ||
			request.InternetAccessible.PublicIpAssigned == nil || *request.InternetAccessible.PublicIpAssigned ||
			request.InternetAccessible.InternetMaxBandwidthOut == nil || *request.InternetAccessible.InternetMaxBandwidthOut != 0 ||
			len(request.LoginSettings) != 1 || request.LoginSettings["KeepImageLogin"] != "true" ||
			len(request.TagSpecification) != 1 || request.TagSpecification[0].ResourceType != "instance" ||
			len(request.TagSpecification[0].Tags) != 1 {
			return false
		}
		tag := request.TagSpecification[0].Tags[0]
		if !f.record(request.ClientToken, tag.Key, tag.Value) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]any{"InstanceIdSet": []string{f.instance + "-second"}, "RequestId": "fixture-launch-receipt"})
		}
		return true
	case r.Host == "cvm.tencentcloudapi.com" && action == "DescribeInstances":
		var query struct {
			Filters []struct {
				Name   string
				Values []string
			}
			Limit int
		}
		marker := f.marker.Load()
		if json.Unmarshal(body, &query) != nil || query.Limit != 2 || len(query.Filters) != 1 ||
			query.Filters[0].Name != "tag:"+fixtureLaunchTag || marker == nil || !slices.Equal(query.Filters[0].Values, []string{*marker}) {
			return false
		}
		row := f.row()
		status := "PENDING"
		if f.ready.Load() {
			status = "RUNNING"
		}
		row["InstanceState"], row["VirtualPrivateCloud"] = status, map[string]string{"VpcId": "vpc-fixture", "SubnetId": f.spec.NetworkID}
		row["InstanceChargeType"], row["SecurityGroupIds"] = "POSTPAID_BY_HOUR", []string{f.spec.SecurityGroup}
		row["Tags"] = []map[string]string{{"Key": fixtureLaunchTag, "Value": *marker}}
		send(map[string]any{"TotalCount": 1, "InstanceSet": []map[string]any{row}})
		return true
	}
	return false
}

func (f *publicCloudCreationFixture) alibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method != http.MethodPost || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) ||
		r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession {
		return false
	}
	action := r.Header.Get("X-ACS-Action")
	if action == "GetCallerIdentity" && r.Host == "sts.aliyuncs.com" {
		testsupport.WriteJSON(t, w, map[string]string{"AccountId": f.account})
		return true
	}
	q := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2014-05-26" || q.Get("RegionId") != f.region {
		return false
	}
	switch action {
	case "RunInstances":
		if q.Get("InstanceName") != f.spec.Name || q.Get("ImageId") != f.spec.ImageID || q.Get("InstanceType") != f.spec.Size ||
			q.Get("VSwitchId") != f.spec.NetworkID || q.Get("SecurityGroupId") != f.spec.SecurityGroup || q.Get("Amount") != "1" ||
			q.Get("InstanceChargeType") != "PostPaid" || q.Get("InternetMaxBandwidthOut") != "0" ||
			q.Get("PasswordInherit") != "false" || q.Has("Password") || q.Get("HttpTokens") != "required" ||
			!f.record(q.Get("ClientToken"), q.Get("Tag.1.Key"), q.Get("Tag.1.Value")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			testsupport.WriteJSON(t, w, map[string]any{"InstanceIdSets": map[string][]string{"InstanceIdSet": {f.instance + "-second"}}, "RequestId": "fixture-launch-receipt"})
		}
	case "DescribeInstances":
		marker := f.marker.Load()
		if q.Get("Tag.1.Key") != fixtureLaunchTag || marker == nil || q.Get("Tag.1.Value") != *marker || q.Get("MaxResults") != "10" {
			return false
		}
		row := f.row()
		status := "Pending"
		if f.ready.Load() {
			status = "Running"
		}
		row["Status"], row["VpcAttributes"] = status, map[string]string{"VpcId": "vpc-fixture", "VSwitchId": f.spec.NetworkID}
		row["RegionId"], row["InstanceChargeType"] = f.region, "PostPaid"
		row["SecurityGroupIds"] = map[string][]string{"SecurityGroupId": {f.spec.SecurityGroup}}
		row["Tags"] = map[string]any{"Tag": []map[string]string{{"TagKey": fixtureLaunchTag, "TagValue": *marker}}}
		testsupport.WriteJSON(t, w, map[string]any{"Instances": map[string]any{"Instance": []map[string]any{row}}})
	default:
		return false
	}
	return true
}

func (f *publicCloudCreationFixture) record(token, key, marker string) bool {
	expected := sha256.Sum256([]byte(strings.Join([]string{fixtureLaunchTag, f.provider, f.account, f.region, token}, "\x00")))
	if len(token) < 16 || key != fixtureLaunchTag || marker != hex.EncodeToString(expected[:]) {
		return false
	}
	f.marker.Store(&marker)
	f.writes.Add(1)
	return true
}

func (f *publicCloudCreationFixture) row() map[string]any {
	image := f.spec.ImageID
	if f.wrongResult.Load() {
		image = "img-other"
	}
	return map[string]any{"InstanceId": f.currentInstance(), "InstanceName": f.spec.Name, "ImageId": image, "InstanceType": f.spec.Size}
}

func (f *publicCloudCreationFixture) currentInstance() string {
	if f.writes.Load() <= 1 {
		return f.instance
	}
	if f.provider == provider.HuaweiECS {
		return fixtureHuaweiSecond
	}
	return f.instance + "-second"
}
