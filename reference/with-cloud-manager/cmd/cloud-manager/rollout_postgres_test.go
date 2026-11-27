package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/delivery"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

const (
	rolloutProject    = "/v1/tenants/tenant/projects/project"
	rolloutOperations = rolloutProject + "/instance-operations"
	rolloutAuditToken = "fixture-rollout-audit-token-over-32-bytes"
)

func TestAcceptanceCloudManagerAPIRolloutAndRollback(t *testing.T) {
	baseline, candidate := os.Getenv("CLOUD_MANAGER_TEST_ROLLOUT_BASELINE"), os.Getenv("CLOUD_MANAGER_TEST_ROLLOUT_CANDIDATE")
	if baseline == "" && candidate == "" {
		t.Skip("provide baseline and candidate cloud-manager binaries to run the rolling rollout drill")
	}
	processWorkers := os.Getenv("CLOUD_MANAGER_TEST_ROLLOUT_PROCESS_WORKERS") == "1"
	if processWorkers && runtime.GOOS != "linux" {
		t.Fatal("worker process rollout requires Linux certificate-file trust")
	}
	baseInfo, baseErr := os.Stat(baseline)
	candidateInfo, candidateErr := os.Stat(candidate)
	if baseErr != nil || candidateErr != nil || os.SameFile(baseInfo, candidateInfo) {
		t.Fatalf("rollout requires distinct binaries: baseline=%v candidate=%v", baseErr, candidateErr)
	}
	targets := priorityRolloutTargets()
	if !processWorkers {
		targets = targets[:1]
	}
	for _, target := range targets {
		t.Run(target.provider, func(t *testing.T) {
			exerciseRolloutAndRollback(t, baseline, candidate, target, processWorkers)
		})
	}
}

type rolloutPowerTarget struct {
	provider, region, account, instance, parked string
	hosts                                       []string
}

func priorityRolloutTargets() []rolloutPowerTarget {
	return []rolloutPowerTarget{
		{provider.TencentCVM, "ap-guangzhou", "100000000001", "ins-rollout", "ins-parked",
			[]string{"cvm.tencentcloudapi.com", "sts.tencentcloudapi.com"}},
		{provider.AlibabaECS, "cn-hangzhou", "1000000000000002", "i-rollout", "i-parked",
			[]string{"ecs.cn-hangzhou.aliyuncs.com", "sts.aliyuncs.com"}},
		{provider.HuaweiECS, "cn-north-4", "1234567890abcdef1234567890abcdef",
			"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002",
			[]string{"ecs.cn-north-4.myhuaweicloud.com", "iam.myhuaweicloud.com"}},
	}
}

func (target rolloutPowerTarget) fixture() *powerFixture {
	return &powerFixture{publicCloudResourceFixture: publicCloudResourceFixture{
		provider: target.provider, account: target.account, region: target.region,
		project: "abcdef1234567890abcdef1234567890", key: "fixture-key", secret: "fixture-secret",
		session: "fixture-session", token: "fixture-iam",
	}, instance: target.instance}
}

func exerciseRolloutAndRollback(t *testing.T, baseline, candidate string, target rolloutPowerTarget, processWorkers bool) {
	t.Helper()
	primary, _, _ := newPITRClusters(t)
	db := primary.start(t)
	rolloutMigrate(t, baseline, primary.dsn())
	cloud := target.fixture()
	seedPowerDrillData(t, db, cloud, target.parked)
	seedRolloutAuditReader(t, db)
	sink, err := delivery.NewDestination(delivery.Config{ID: "rollout-fixture", Tenant: "tenant", Project: "project",
		Endpoint: "https://receiver.invalid/events", Secret: "fixture-rollout-event-key-over-32-bytes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.NewStore(db).Register(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	old := startRolloutAPI(t, baseline, primary.dsn(), rolloutAddress(t))
	old.waitProbe(t, "/readyz", http.StatusOK)
	profile := pitrWorkerProfile(t, cloud)
	startWorker := rolloutWorkerStarter(t, db, primary.dsn(), profile, cloud, target.hosts, processWorkers)
	submit := func(api rolloutAPI, action, asset, key string) operations.Operation {
		t.Helper()
		payload, err := json.Marshal(operations.Input{Account: cloud.provider, Region: cloud.region, AssetID: asset, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return decodeRollout[operations.Operation](t, api, http.MethodPost, rolloutOperations, key, payload, http.StatusAccepted)
	}
	oldWorker := startWorker(baseline, "dispatch")
	first := submit(old, "start", cloud.instance, "baseline-start")
	waitPITRDone(t, db, first.ID)
	oldWorker()
	previousDelivery, originalBody := rolloutDelivery(t, db, first.ID)
	originalTimeline := rolloutTimeline(t, old, first.ID, "succeeded")
	assertRolloutEffectID(t, originalBody, "")
	queued := submit(old, "start", target.parked, "carry-queued")
	pitrExec(t, db, "UPDATE cmp_operation_tasks SET available_at=clock_timestamp()+interval '1 hour' WHERE id=$1", queued.ID)

	next := startRolloutAPI(t, candidate, primary.dsn(), rolloutAddress(t))
	next.waitProbe(t, "/readyz", http.StatusServiceUnavailable)
	migrationStart := time.Now()
	reads := rolloutMigrateWithReads(t, old, candidate, primary.dsn(), rolloutOperations+"/"+first.ID)
	old.waitProbe(t, "/readyz", http.StatusOK)
	next.waitProbe(t, "/readyz", http.StatusOK)
	t.Logf("additive migration and candidate readiness=%s, baseline read probes=%d", time.Since(migrationStart).Round(time.Millisecond), reads)
	assertRolloutOperation(t, next, first.ID, "succeeded")
	if !slices.Equal(rolloutTimeline(t, next, first.ID, "succeeded"), originalTimeline) {
		t.Fatal("candidate returned a different baseline operation timeline")
	}
	if got := submit(next, "start", target.parked, "carry-queued"); got.ID != queued.ID || got.Status != queued.Status ||
		got.Action != queued.Action || got.AssetID != queued.AssetID || !got.CreatedAt.Equal(queued.CreatedAt) {
		t.Fatalf("candidate did not reuse the old queued receipt: %+v", got)
	}
	if observedID, body := rolloutDelivery(t, db, first.ID); observedID != previousDelivery || !bytes.Equal(body, originalBody) {
		t.Fatal("migration modified the original delivery")
	}

	newWorker := startWorker(candidate, "dispatch")
	second := submit(next, "stop", cloud.instance, "candidate-stop")
	waitPITRDone(t, db, second.ID)
	newWorker()
	assertRolloutOperation(t, old, second.ID, "succeeded")
	candidateDelivery, candidateBody := rolloutDelivery(t, db, second.ID)
	candidateTimeline := rolloutTimeline(t, next, second.ID, "succeeded")
	assertRolloutEffectID(t, candidateBody, "cmp-op-final-v1:"+second.ID)
	pendingRollback := submit(next, "start", cloud.instance, "candidate-queued")
	assertPITREvidence(t, db, pendingRollback.ID, "queued", "queued", false)
	if processWorkers {
		observer := startWorker(candidate, "observe-only")
		waitRolloutObservationPoll(t, db)
		observer()
		assertUnclaimedRolloutTask(t, db, pendingRollback.ID, true)
		t.Log("observation worker completed its queue poll without claiming due work")
	}

	rollbackStarted := time.Now()
	next.stop()
	rolledBack := startRolloutAPI(t, baseline, primary.dsn(), next.address)
	rolledBack.waitProbe(t, "/readyz", http.StatusOK)
	t.Logf("application rollback readiness=%s", time.Since(rollbackStarted).Round(time.Millisecond))
	assertRolloutOperation(t, rolledBack, first.ID, "succeeded")
	assertRolloutOperation(t, rolledBack, second.ID, "succeeded")
	if !slices.Equal(rolloutTimeline(t, rolledBack, first.ID, "succeeded"), originalTimeline) ||
		!slices.Equal(rolloutTimeline(t, rolledBack, second.ID, "succeeded"), candidateTimeline) {
		t.Fatal("application rollback changed an operation timeline")
	}
	if got := submit(rolledBack, "stop", cloud.instance, "candidate-stop"); got.ID != second.ID || got.Status != "succeeded" {
		t.Fatalf("rolled-back API did not reuse the candidate receipt: %+v", got)
	}
	assertRolloutOperation(t, rolledBack, queued.ID, "queued")
	assertPITREvidence(t, db, queued.ID, "queued", "queued", false)
	assertUnclaimedRolloutTask(t, db, queued.ID, false)
	if got := submit(rolledBack, "start", cloud.instance, "candidate-queued"); got.ID != pendingRollback.ID || got.Status != "queued" {
		t.Fatalf("rolled-back API did not reuse the candidate's queued receipt: %+v", got)
	}
	if cloud.writes.Load() != 2 || cloud.running.Load() {
		t.Fatal("a provider effect occurred while the workers were stopped")
	}
	rollbackWorker := startWorker(baseline, "dispatch")
	waitPITRDone(t, db, pendingRollback.ID)
	rollbackWorker()
	assertRolloutOperation(t, rolledBack, pendingRollback.ID, "succeeded")
	_, rollbackBody := rolloutDelivery(t, db, pendingRollback.ID)
	assertRolloutEffectID(t, rollbackBody, "cmp-op-final-v1:"+pendingRollback.ID)
	assertPITREvidence(t, db, queued.ID, "queued", "queued", false)
	assertUnclaimedRolloutTask(t, db, queued.ID, false)
	if observedID, body := rolloutDelivery(t, db, first.ID); observedID != previousDelivery || !bytes.Equal(body, originalBody) {
		t.Fatal("application rollback modified the baseline delivery")
	}
	if observedID, body := rolloutDelivery(t, db, second.ID); observedID != candidateDelivery || !bytes.Equal(body, candidateBody) {
		t.Fatal("application rollback modified the candidate delivery")
	}
	var migrated bool
	if err := db.QueryRowContext(t.Context(), "SELECT EXISTS (SELECT 1 FROM cmp_migrations WHERE version=$1)", operations.TerminalEventTimeMigration).
		Scan(&migrated); err != nil || !migrated {
		t.Fatalf("candidate migration marker did not survive application rollback: %v", err)
	}
	if cloud.writes.Load() != 3 || !cloud.running.Load() {
		t.Fatal("rollout or rollback repeated a provider effect")
	}
	t.Logf("candidate work completed after rollback; independent worker processes=%t", processWorkers)
}

func rolloutWorkerStarter(t *testing.T, db *sql.DB, dsn, profile string, cloud *powerFixture, hosts []string, processWorkers bool) func(string, string) func() {
	t.Helper()
	if processWorkers {
		return rolloutWorkerProcesses(t, dsn, profile, cloud, hosts)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cloud.serve(t, w, r) }))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, server.Client().Transport}
	return func(_ string, mode string) func() { return startPITRWorker(t, db, profile, mode, transport) }
}

func decodeRollout[T any](t *testing.T, api rolloutAPI, method, path, key string, payload []byte, want int) T {
	t.Helper()
	code, body, err := api.request(t.Context(), method, path, key, payload)
	if err != nil || code != want {
		t.Fatalf("rollout request %s %s returned %d, want %d: %v %s", method, path, code, want, err, body)
	}
	var envelope struct{ Data T }
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func assertRolloutOperation(t *testing.T, api rolloutAPI, id, status string) {
	t.Helper()
	item := decodeRollout[operations.Operation](t, api, http.MethodGet, rolloutOperations+"/"+id, "", nil, http.StatusOK)
	if item.ID != id || item.Status != status {
		t.Fatalf("rollout receipt changed: %+v", item)
	}
	query := "/audit?subject_id=" + url.QueryEscape(id)
	assertRolloutAccessStatus(t, api, rolloutProject+query, http.StatusForbidden)
	api.credential = rolloutAuditToken
	assertRolloutAccessStatus(t, api, "/v1/tenants/tenant/projects/unassigned"+query, http.StatusForbidden)
	isolated := decodeRollout[audit.Page](t, api, http.MethodGet, "/v1/tenants/tenant/projects/isolated"+query, "", nil, http.StatusOK)
	if len(isolated.Items) != 0 || isolated.Next != "" {
		t.Fatal("rollout audit returned another project's entries")
	}
	page := decodeRollout[audit.Page](t, api, http.MethodGet, rolloutProject+query, "", nil, http.StatusOK)
	for _, entry := range page.Items {
		if entry.SubjectID == id && entry.Outcome == status {
			return
		}
	}
	t.Fatalf("rollout API cannot read the operation audit for status %s", status)
}

func assertRolloutAccessStatus(t *testing.T, api rolloutAPI, path string, want int) {
	t.Helper()
	status, _, err := api.request(t.Context(), http.MethodGet, path, "", nil)
	if err != nil || status != want {
		t.Fatalf("rollout authorization for %s: status=%d, want %d: %v", path, status, want, err)
	}
}

func rolloutTimeline(t *testing.T, api rolloutAPI, id, finalStatus string) []operations.OperationEvent {
	t.Helper()
	events := decodeRollout[operations.EventPage](t, api, http.MethodGet,
		rolloutOperations+"/"+id+"/events", "", nil, http.StatusOK)
	if events.HasMore || len(events.Events) == 0 {
		t.Fatalf("rollout operation timeline is incomplete: %+v", events)
	}
	last := events.Events[len(events.Events)-1]
	if last.Status != finalStatus || events.NextAfter != last.Sequence {
		t.Fatalf("rollout operation timeline lacks the final status: %+v", events)
	}
	return events.Events
}

func seedRolloutAuditReader(t *testing.T, db *sql.DB) {
	t.Helper()
	pitrExec(t, db, `INSERT INTO cmp_projects VALUES ('tenant','unassigned'),('tenant','isolated');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','audit-admin','service','fixture','audit-admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','audit-admin','admin'),('tenant','isolated','audit-admin','admin')`)
	hash := sha256.Sum256([]byte(rolloutAuditToken))
	pitrExec(t, db, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','audit-admin',now()+interval '1 hour')`, hash[:])
}

func rolloutDelivery(t *testing.T, db *sql.DB, operation string) (string, []byte) {
	t.Helper()
	var id string
	var body []byte
	if err := db.QueryRowContext(t.Context(), `SELECT box.public_id::text,box.body
		FROM cmp_operation_event_outbox box JOIN cmp_instance_operation_events e ON e.event_id=box.sequence
		WHERE box.sink_id='rollout-fixture' AND e.operation_id=$1 AND e.status='succeeded'`, operation).
		Scan(&id, &body); err != nil {
		t.Fatal(err)
	}
	return id, body
}

func assertRolloutEffectID(t *testing.T, body []byte, want string) {
	t.Helper()
	var payload struct {
		Effect string `json:"effect_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Effect != want {
		t.Fatalf("unexpected effect identifier in rollout delivery: %v", err)
	}
}
