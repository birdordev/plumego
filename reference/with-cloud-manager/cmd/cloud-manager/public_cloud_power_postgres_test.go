package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
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

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudInstancePower(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actorToken = "fixture-power-operator-service-token"
	const reviewerToken = "fixture-power-approver-service-token"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','operator','operator'),('tenant','project','reviewer','approver');
		INSERT INTO cmp_instance_approval_rules (tenant_id,project_id,action) VALUES ('tenant','project','stop'),('tenant','project','reboot');`)
	for _, entry := range []struct{ role, token string }{{"operator", actorToken}, {"reviewer", reviewerToken}} {
		digest := sha256.Sum256([]byte(entry.token))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, digest[:], entry.role)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}

	for _, item := range []struct{ name, region, account, instance string }{
		{provider.TencentCVM, "ap-guangzhou", "100000000001", "ins-test"},
		{provider.AlibabaECS, "cn-hangzhou", "1000000000000002", "i-test"},
		{provider.HuaweiECS, "cn-north-4", "1234567890abcdef1234567890abcdef", "00000000-0000-4000-8000-000000000001"},
	} {
		t.Run(item.name, func(t *testing.T) {
			fixture := powerFixture{
				publicCloudResourceFixture: publicCloudResourceFixture{
					provider: item.name, account: item.account, region: item.region,
					project: "abcdef1234567890abcdef1234567890", key: "fixture-key", secret: "fixture-secret",
					session: "fixture-session", token: "fixture-iam",
				},
				instance: item.instance,
				beforeRead: func(ctx context.Context) error {
					_, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET revision=revision+1
						WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')`)
					return err
				},
			}
			profile := publicCloudProfile{Ref: "fixture/" + item.name, Config: publiccloud.Config{
				Regions: []string{item.region}, AccountID: item.account,
			}}
			remoteIdentity := item.account
			if item.name == provider.HuaweiECS {
				profile.ProjectID, profile.IAMToken = fixture.project, fixture.token
				remoteIdentity += ":" + fixture.project
			} else {
				profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixture.key, fixture.secret, fixture.session
			}
			exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
				VALUES ('tenant',$1,'project',$1,$2,$3,'write_enabled')`, item.name, profile.Ref, remoteIdentity)
			exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
				VALUES ('tenant',$1,$2,'instance',1,now())`, item.name, item.region)
			exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
				SELECT scope_id,$3,'fixture-instance','stopped',1 FROM cmp_inventory_scopes
				WHERE account_id=$1 AND region=$2 AND kind='instance'`, item.name, item.region, fixture.instance)

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
			saveProfile := func(allowPower, allowReboot, allowDelete bool, filename string) string {
				t.Helper()
				profile.AllowInstancePower, profile.AllowInstanceReboot = allowPower, allowReboot
				profile.AllowVolumeChanges = allowReboot
				profile.AllowInstanceDelete = allowDelete
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
			allow, deny := saveProfile(true, false, false, "allow.json"), saveProfile(false, false, false, "deny.json")
			rebootProfile := saveProfile(true, true, false, "reboot.json")
			deletionProfile := saveProfile(true, true, true, "delete.json")
			var enabled, rebootEnabled, deletionEnabled atomic.Bool
			workerCtx, cancel := context.WithCancel(t.Context())
			workerDone := make(chan error, 1)
			go func() {
				workerDone <- store.RunWorker(workerCtx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
					path := deny
					if enabled.Load() {
						path = allow
					}
					if rebootEnabled.Load() {
						path = rebootProfile
					}
					if deletionEnabled.Load() {
						path = deletionProfile
					}
					return connectPublicCloudOperator(ctx, binding, path, transport)
				})
			}()
			defer func() {
				cancel()
				select {
				case err := <-workerDone:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("worker did not stop")
				}
			}()
			submit := func(action, key string, code int) operations.Operation {
				t.Helper()
				return submitFixturePower(t, app, item.name, item.region, fixture.instance, actorToken, action, key, code)
			}
			wait := func(id, state string, advance bool) operations.Operation {
				t.Helper()
				return waitFixturePower(t, db, store, scope, actorToken, id, state, advance)
			}
			approve := func(op operations.Operation) {
				t.Helper()
				if op.Status != "awaiting_approval" {
					t.Fatalf("missing approval state: %+v", op)
				}
				if _, err := store.Approve(t.Context(), reviewerToken, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
					t.Fatal(err)
				}
			}

			disabled := submit("start", item.name+"-disabled", http.StatusAccepted)
			if failed := wait(disabled.ID, "failed", false); failed.OutcomeReason != "provider_power_disabled" ||
				fixture.writes.Load() != 0 || fixture.reads.Load() != 0 {
				t.Fatalf("default write guard: %+v", failed)
			}

			enabled.Store(true)
			start := submit("start", item.name+"-start", http.StatusAccepted)
			accepted := wait(start.ID, "observing", false)
			if accepted.OutcomeReason != "provider_accepted" || accepted.ProviderRequestID != "fixture-power-receipt" {
				t.Fatalf("start receipt: %+v", accepted)
			}
			exec(`UPDATE cmp_inventory_scopes SET running=true,cursor='stale-fixture',pages=1, fence=17
				WHERE account_id=$1 AND kind='instance'`, item.name)
			wait(start.ID, "succeeded", true)
			var powerState, cursor string
			var running bool
			var fence int64
			if err := db.QueryRowContext(ctx, `SELECT o.status,s.cursor,s.running,s.fence
				FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
				WHERE s.account_id=$1 AND o.external_id=$2 AND s.kind='instance'`, item.name, fixture.instance).
				Scan(&powerState, &cursor, &running, &fence); err != nil ||
				(!strings.EqualFold(powerState, "running") && !strings.EqualFold(powerState, "active")) ||
				cursor != "" || running || fence != 18 {
				t.Fatalf("start status projection: state %s, cursor %q, running %t, fence %d, error %v", powerState, cursor, running, fence, err)
			}

			rebootDisabled := submit("reboot", item.name+"-reboot-disabled", http.StatusAccepted)
			approve(rebootDisabled)
			if failed := wait(rebootDisabled.ID, "failed", false); failed.OutcomeReason != "provider_power_disabled" || fixture.reboots.Load() != 0 {
				t.Fatalf("independent reboot guard: %+v", failed)
			}
			rebootEnabled.Store(true)
			reboot := submit("reboot", item.name+"-reboot", http.StatusAccepted)
			if fixture.reboots.Load() != 0 {
				t.Fatal("reboot dispatched before approval")
			}
			approve(reboot)
			if item.name == provider.AlibabaECS {
				review := wait(reboot.ID, "needs_review", false)
				if review.ProviderRequestID != fixtureRebootReceipt || review.OutcomeReason != "reboot_completion_requires_evidence" {
					t.Fatalf("manual reboot evidence: %+v", review)
				}
				resolution := operations.Resolution{Decision: "succeeded", EvidenceRef: "verify/fixture-reboot", ProviderRequestID: "foreign-receipt"}
				if _, err := store.Resolve(ctx, reviewerToken, scope, reboot.ID, resolution); !errors.Is(err, operations.ErrConflict) {
					t.Fatalf("foreign reboot evidence accepted: %v", err)
				}
				resolution.ProviderRequestID = fixtureRebootReceipt
				if _, err := store.Resolve(ctx, reviewerToken, scope, reboot.ID, resolution); err != nil {
					t.Fatal(err)
				}
			} else {
				observation := wait(reboot.ID, "observing", false)
				if observation.ProviderRequestID != fixtureRebootReceipt {
					t.Fatalf("lost correlated reboot receipt: %+v", observation)
				}
				waitPendingCloudObservation(t, db, reboot.ID)
				fixture.rebootReady.Store(true)
			}
			wait(reboot.ID, "succeeded", true)
			if fixture.reboots.Load() != 1 {
				t.Fatalf("reboot request sent %d times", fixture.reboots.Load())
			}

			fixture.exerciseVolumeChanges(t, db, store, app, scope, actorToken, reviewerToken)

			stop := submit("stop", item.name+"-stop", http.StatusAccepted)
			if fixture.writes.Load() != 1 {
				t.Fatal("sent stop before approval")
			}
			approve(stop)
			waiting := wait(stop.ID, "observing", false)
			if waiting.OutcomeReason != "submission_outcome_unknown" || waiting.ProviderRequestID != "" {
				t.Fatalf("stop response loss: %+v", waiting)
			}
			wait(stop.ID, "succeeded", true)
			if err := db.QueryRowContext(ctx, `SELECT o.status FROM cmp_inventory_objects AS o
				JOIN cmp_inventory_scopes AS s USING (scope_id) WHERE s.account_id=$1 AND o.external_id=$2 AND s.kind='instance'`,
				item.name, fixture.instance).Scan(&powerState); err != nil ||
				(!strings.EqualFold(powerState, "stopped") && !strings.EqualFold(powerState, "shutoff")) {
				t.Fatalf("stop status projection: state %s, error %v", powerState, err)
			}
			if fixture.writes.Load() != 2 || fixture.running.Load() {
				t.Fatalf("incorrect final provider state, writes %d", fixture.writes.Load())
			}

			fixture.wrongScope.Store(true)
			foreign := submit("start", item.name+"-foreign", http.StatusAccepted)
			if failed := wait(foreign.ID, "failed", false); failed.OutcomeReason != "power_target_unconfirmed" {
				t.Fatalf("foreign target: %+v", failed)
			}
			fixture.wrongScope.Store(false)

			revoked := submit("stop", item.name+"-revoked", http.StatusAccepted)
			fixture.revokeOnRead.Store(true)
			approve(revoked)
			if failed := wait(revoked.ID, "failed", false); failed.OutcomeReason != "dispatch_unavailable" || fixture.writes.Load() != 2 {
				t.Fatalf("revoked approver still allowed a write: %+v", failed)
			}
			fixture.exerciseDeletion(t, db, store, app, scope, actorToken, reviewerToken, &deletionEnabled)
		})
	}
}

func submitFixturePower(t *testing.T, app http.Handler, account, region, id, token, action, key string, code int) operations.Operation {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/instance-operations",
		strings.NewReader(fmt.Sprintf(`{"account":%q,"region":%q,"asset_id":%q,"action":%q}`, account, region, id, action)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", `"1"`)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != code {
		t.Fatalf("submit %s status %d, want %d: %s", action, w.Code, code, w.Body)
	}
	var payload struct{ Data operations.Operation }
	if code == http.StatusAccepted {
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.Data.ID == "" {
			t.Fatalf("operation receipt: %s %v", w.Body, err)
		}
	}
	return payload.Data
}

func waitFixturePower(t *testing.T, db *sql.DB, store operations.Store, scope identity.Scope, token, id, state string, advance bool) operations.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err := store.Get(ctx, token, scope, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status == state {
			return op
		}
		if advance {
			if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
				WHERE id=$1 AND status='queued' AND available_at>clock_timestamp()`, id); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("operation %s status %q, want %q", id, op.Status, state)
		case <-ticker.C:
		}
	}
}

const fixtureRebootReceipt = "fixture-reboot-receipt"

func waitPendingCloudObservation(t *testing.T, db *sql.DB, operationID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		var attempts int
		if err := db.QueryRowContext(ctx, "SELECT status,attempts FROM cmp_operation_tasks WHERE id=$1", operationID).
			Scan(&state, &attempts); err != nil {
			t.Fatal(err)
		}
		if state == "done" || state == "dead" {
			t.Fatalf("operation finalized without matching completion evidence, queue status %s", state)
		}
		if state == "queued" && attempts >= 2 {
			return
		}
		if attempts == 1 && state == "queued" {
			if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
				WHERE id=$1 AND status='queued' AND attempts=1 AND available_at>clock_timestamp()`, operationID); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation evidence was not checked")
		case <-ticker.C:
		}
	}
}

type powerFixture struct {
	publicCloudResourceFixture
	volumeMutationFixture
	deletionFixture
	instance                                       string
	beforeRead                                     func(context.Context) error
	running, wrongScope, revokeOnRead, rebootReady atomic.Bool
	writes, reads, reboots                         atomic.Int32
}

func (f *powerFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if f.serveDeletion(t, w, r) || f.serveVolumeMutation(t, w, r) {
		return
	}
	write := func(value any) {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}
	bad := func() {
		t.Errorf("invalid power request for host %s path %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
	var action string
	switch f.provider {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session {
			bad()
			return
		}
		if r.Host == "sts.tencentcloudapi.com" && r.Header.Get("X-TC-Action") == "GetCallerIdentity" {
			write(map[string]any{"Response": map[string]string{"AccountId": f.account}})
			return
		}
		action = r.Header.Get("X-TC-Action")
		var target struct {
			InstanceIds           []string
			StopType, StoppedMode string
		}
		if r.Host != "cvm.tencentcloudapi.com" || r.Header.Get("X-TC-Version") != "2017-03-12" ||
			json.Unmarshal(body, &target) != nil || !slices.Equal(target.InstanceIds, []string{f.instance}) ||
			(action == "StopInstances" && (target.StopType != "SOFT" || target.StoppedMode != "KEEP_CHARGING")) ||
			(action == "RebootInstances" && (target.StopType != "SOFT" || target.StoppedMode != "")) {
			bad()
			return
		}
	case provider.AlibabaECS:
		if !validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Security-Token") != f.session {
			bad()
			return
		}
		action = r.Header.Get("X-ACS-Action")
		if r.Host == "sts.aliyuncs.com" && action == "GetCallerIdentity" {
			write(map[string]string{"AccountId": f.account})
			return
		}
		q := r.URL.Query()
		if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			bad()
			return
		}
		if action == "DescribeInstances" {
			if q.Get("InstanceIds") != fmt.Sprintf(`[%q]`, f.instance) || q.Get("RegionId") != f.region {
				bad()
				return
			}
		} else if q.Get("InstanceId") != f.instance || (action == "StopInstance" &&
			(q.Get("ForceStop") != "false" || q.Get("StoppedMode") != "KeepCharging")) ||
			(action == "RebootInstance" && (q.Get("ForceStop") != "false" || q.Get("StoppedMode") != "")) {
			bad()
			return
		}
	case provider.HuaweiECS:
		if r.Header.Get("X-Auth-Token") != f.token {
			bad()
			return
		}
		if r.Host == "iam.myhuaweicloud.com" && r.URL.Path == "/v3/auth/tokens" &&
			r.Header.Get("X-Subject-Token") == f.token {
			write(map[string]any{"token": map[string]any{"expires_at": time.Now().Add(time.Hour).UTC(),
				"project": map[string]any{"id": f.project, "domain": map[string]string{"id": f.account}}}})
			return
		}
		base := "/v1/" + f.project + "/cloudservers/"
		switch {
		case r.Host != "ecs."+f.region+".myhuaweicloud.com":
			bad()
			return
		case r.Method == http.MethodGet && r.URL.Path == base+f.instance:
			action = "DescribeInstances"
		case r.Method == http.MethodGet && r.URL.Path == "/v1/"+f.project+"/jobs/"+fixtureRebootReceipt:
			result := map[string]any{"job_id": fixtureRebootReceipt, "status": "RUNNING"}
			if f.rebootReady.Load() {
				result["status"] = "SUCCESS"
				result["entities"] = map[string]any{"sub_jobs_total": 1, "sub_jobs": []any{map[string]any{
					"status": "SUCCESS", "entities": map[string]string{"server_id": f.instance},
				}}}
			}
			write(result)
			return
		case r.Method == http.MethodPost && r.URL.Path == base+"action":
			var body map[string]struct {
				Servers []struct{ ID string }
				Type    string
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				bad()
				return
			}
			for op, target := range body {
				if (op != "os-start" && op != "os-stop" && op != "reboot") || len(target.Servers) != 1 || target.Servers[0].ID != f.instance ||
					((op == "os-stop" || op == "reboot") && target.Type != "SOFT") {
					bad()
					return
				}
				action = op
			}
		default:
			bad()
			return
		}
	}

	switch action {
	case "DescribeInstances":
		f.reads.Add(1)
		if f.revokeOnRead.CompareAndSwap(true, false) {
			if err := f.beforeRead(r.Context()); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		write(f.instanceResponse())
	case "StartInstances", "StartInstance", "os-start":
		f.writes.Add(1)
		f.running.Store(true)
		const receipt = "fixture-power-receipt"
		switch f.provider {
		case provider.TencentCVM:
			write(map[string]any{"Response": map[string]string{"RequestId": receipt}})
		case provider.AlibabaECS:
			write(map[string]string{"RequestId": receipt})
		case provider.HuaweiECS:
			write(map[string]string{"job_id": receipt})
		}
	case "RebootInstances", "RebootInstance", "reboot":
		f.reboots.Add(1)
		switch f.provider {
		case provider.TencentCVM:
			write(map[string]any{"Response": map[string]string{"RequestId": fixtureRebootReceipt}})
		case provider.AlibabaECS:
			write(map[string]string{"RequestId": fixtureRebootReceipt})
		case provider.HuaweiECS:
			write(map[string]string{"job_id": fixtureRebootReceipt})
		}
	case "StopInstances", "StopInstance", "os-stop":
		f.writes.Add(1)
		f.running.Store(false)
		interruptFixtureResponse(t, w)
	default:
		bad()
	}
}

func (f *powerFixture) instanceResponse() any {
	switch f.provider {
	case provider.TencentCVM:
		id, status := f.instance, "STOPPED"
		if f.wrongScope.Load() {
			id = "ins-foreign"
		}
		if f.running.Load() {
			status = "RUNNING"
		}
		row := map[string]any{"InstanceId": id, "InstanceState": status, "Placement": map[string]string{"Zone": f.region + "-1"}}
		if f.volumeAttached.Load() {
			device := "DataDisks"
			if f.volumeRoot.Load() {
				device = "SystemDisk"
				row[device] = map[string]string{"DiskId": f.volumeID()}
			} else {
				row[device] = []any{map[string]string{"DiskId": f.volumeID()}}
			}
		}
		if f.reboots.Load() != 0 {
			row["LatestOperation"], row["LatestOperationState"] = "RebootInstances", "SUCCESS"
			row["LatestOperationRequestId"] = "fixture-older-reboot"
			if f.rebootReady.Load() {
				row["LatestOperationRequestId"] = fixtureRebootReceipt
			}
		}
		return map[string]any{"Response": map[string]any{"TotalCount": 1, "InstanceSet": []any{row}}}
	case provider.AlibabaECS:
		region, status := f.region, "Stopped"
		if f.wrongScope.Load() {
			region = "cn-beijing"
		}
		if f.running.Load() {
			status = "Running"
		}
		return map[string]any{"Instances": map[string]any{"Instance": []any{map[string]string{
			"InstanceId": f.instance, "Status": status, "RegionId": region, "ZoneId": f.region + "-a",
		}}}}
	default:
		project, status := f.project, "SHUTOFF"
		if f.wrongScope.Load() {
			project = "00000000000000000000000000000000"
		}
		if f.running.Load() {
			status = "ACTIVE"
		}
		attachments := []map[string]any{}
		if f.volumeAttached.Load() || f.heldAttachment.Load() {
			device, index := "/dev/sdf", "-1"
			if f.volumeRoot.Load() {
				device, index = "/dev/sda", "0"
			}
			attachments = append(attachments, map[string]any{
				"id": f.volumeID(), "device": device, "bootIndex": index, "delete_on_termination": "false",
			})
		}
		return map[string]any{"server": map[string]any{"id": f.instance, "tenant_id": project, "status": status,
			"OS-EXT-AZ:availability_zone": f.region + "a", "OS-EXT-SRV-ATTR:root_device_name": "/dev/sda",
			"os-extended-volumes:volumes_attached": attachments,
		}}
	}
}
