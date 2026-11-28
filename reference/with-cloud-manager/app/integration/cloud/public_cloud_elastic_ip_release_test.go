package cloudtest

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func (f *vpcCreationFixture) exerciseElasticIPRelease(t *testing.T, db *sql.DB, store operations.Store, app http.Handler,
	scope identity.Scope, actor, reviewer string, enabled *atomic.Bool) {
	t.Helper()
	ctx := t.Context()
	f.releaseMode.Store(true)
	const route = "/v1/tenants/tenant/projects/project/network-operations"
	input := operations.ElasticIPRelease{Account: f.vendor, Region: f.region, AssetID: f.second, PublicAddress: fixtureAllocatedSecondIP}
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT a.revision FROM cmp_asset_ownership AS a JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (s.account_id,s.kind,a.external_id)=($1,'elastic-ip',$2)`, f.vendor, f.second).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	match := strconv.Quote(strconv.FormatInt(revision, 10))
	submit := func(key, revision, address string, expected int) operations.Operation {
		t.Helper()
		req := input
		req.PublicAddress = address
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, route+"/release-elastic-ip", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		if revision != "" {
			r.Header.Set("If-Match", revision)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != expected {
			t.Fatalf("%s returned %d, want %d: %s", key, w.Code, expected, w.Body)
		}
		var op operations.Operation
		if w.Code == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.ID == "" || op.Action != "release-elastic-ip" || op.AssetID != f.second ||
				op.ResourceKind != provider.ElasticIP || w.Header().Get("Location") != route+"/"+op.ID {
				t.Fatalf("invalid release receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if op.Status != "awaiting_approval" {
			t.Fatalf("release skipped review: %+v", op)
		}
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, state, advance)
	}
	assertDeclined := func(key, reason string) {
		t.Helper()
		op := submit(key, match, input.PublicAddress, http.StatusAccepted)
		approve(op)
		if declined := wait(op, "failed", false); declined.OutcomeReason != reason || f.releases.Load() != 0 {
			t.Fatalf("release guard %s: %+v", key, declined)
		}
	}

	submit("release-missing-revision", "", input.PublicAddress, http.StatusPreconditionFailed)
	submit("release-stale", strconv.Quote(strconv.FormatInt(revision+1, 10)), input.PublicAddress, http.StatusPreconditionFailed)
	submit("release-wrong-address", match, fixtureAllocatedFirstIP, http.StatusConflict)
	assertDeclined("release-disabled", "elastic_ip_release_disabled")
	enabled.Store(true)
	f.blockedGroup.Store(true)
	assertDeclined("release-remotely-bound", "elastic_ip_release_preconditions_unconfirmed")
	f.blockedGroup.Store(false)
	f.protectedIP.Store(true)
	assertDeclined("release-protected", "elastic_ip_release_preconditions_unconfirmed")
	f.protectedIP.Store(false)
	f.badEvidence.Store(true)
	assertDeclined("release-remote-address-mismatch", "elastic_ip_release_preflight_unavailable")
	f.badEvidence.Store(false)
	f.revokeOnIdentity.Store(true)
	assertDeclined("release-stale-approval", "dispatch_unavailable")

	op := submit("release", match, input.PublicAddress, http.StatusAccepted)
	if replay := submit("release", match, input.PublicAddress, http.StatusAccepted); replay.ID != op.ID {
		t.Fatal("release did not replay the original operation")
	}
	submit("release", match, fixtureAllocatedFirstIP, http.StatusConflict)
	submit("release-overlap", match, input.PublicAddress, http.StatusConflict)
	stored, err := store.Get(ctx, reviewer, scope, op.ID)
	if err != nil || stored.NetworkIntent == nil || stored.NetworkIntent.PublicAddress != input.PublicAddress ||
		stored.NetworkIntent.AssetRevision != revision || stored.NetworkIntent.Name != fixtureVPCName {
		t.Fatalf("approval intent lost asset identity: %+v %v", stored, err)
	}
	approve(op)
	if observing := wait(op, "observing", false); observing.OutcomeReason != "submission_outcome_unknown" || f.releases.Load() != 1 {
		t.Fatalf("release did not retain ambiguous outcome: %+v", observing)
	}

	f.visible.Store(false)
	waitPendingCloudObservation(t, db, op.ID)
	var observations int
	if err := db.QueryRowContext(ctx, "SELECT absence_observations FROM cmp_network_intents WHERE operation_id=$1", op.ID).
		Scan(&observations); err != nil || observations != 1 {
		t.Fatalf("first complete absence evidence: %d %v", observations, err)
	}
	f.invalidAbsence.Store(true)
	if review := wait(op, "needs_review", true); review.OutcomeReason != "elastic_ip_release_requires_evidence" {
		t.Fatalf("incomplete absence evidence settled release: %+v", review)
	}
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{
		Decision: "succeeded", EvidenceRef: "fixture/unverified-release",
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("unverified release marked succeeded: %v", err)
	}
	f.invalidAbsence.Store(false)
	enabled.Store(false)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID,
		operations.ObservationRetry{EvidenceRef: "fixture/elastic-ip-release"}); err != nil {
		t.Fatal(err)
	}
	if succeeded := wait(op, "succeeded", true); succeeded.OutcomeReason != "elastic_ip_absence_confirmed" || f.releases.Load() != 1 {
		t.Fatalf("read-only recovery after ambiguous release: %+v", succeeded)
	}
	var deleted, terminalEvent bool
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT absence_observations FROM cmp_network_intents WHERE operation_id=$1),
		EXISTS(SELECT 1 FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
			WHERE (s.account_id,s.kind,o.external_id)=($2,'elastic-ip',$3) AND o.removed_at IS NOT NULL AND o.deletion_confirmed_at IS NOT NULL),
		EXISTS(SELECT 1 FROM cmp_operation_event_outbox AS d JOIN cmp_instance_operation_events AS e ON e.event_id=d.sequence
			WHERE d.event_type='elastic-ip.operation.succeeded' AND e.operation_id=$1)`, op.ID, f.vendor, f.second).
		Scan(&observations, &deleted, &terminalEvent); err != nil || observations != 2 || !deleted || !terminalEvent {
		t.Fatalf("release result did not commit atomically: %d %t %t %v", observations, deleted, terminalEvent, err)
	}
	page, err := store.List(ctx, actor, scope, operations.Query{Kind: provider.ElasticIP, Action: "release-elastic-ip", Status: "succeeded", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != op.ID {
		t.Fatalf("release missing from operation list: %+v %v", page, err)
	}
	legacy, err := json.Marshal(struct {
		operations.ElasticIPRelease
		Revision int64
	}{input, revision})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("release-elastic-ip\x00"), legacy...))
	var compatible bool
	if err := db.QueryRowContext(ctx, "SELECT request_hash=$2 FROM cmp_instance_operations WHERE operation_id=$1", op.ID, digest[:]).
		Scan(&compatible); err != nil || !compatible {
		t.Fatalf("release operation idempotency fingerprint changed after upgrade: %t %v", compatible, err)
	}
	if replay := submit("release", match, input.PublicAddress, http.StatusAccepted); replay.ID != op.ID || replay.Status != "succeeded" {
		t.Fatalf("release replay after asset removal: %+v", replay)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"release-elastic-ip", scope.Tenant, scope.Project, "--idempotency-key", "release", "--if-match", strconv.FormatInt(revision, 10)}
	if err := commands.RunOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body), &output, nil); err != nil {
		t.Fatal(err)
	}
	var replay operations.Operation
	if err := json.Unmarshal(output.Bytes(), &replay); err != nil || replay.ID != op.ID || replay.Status != "succeeded" {
		t.Fatalf("CLI release replay: %+v %v", replay, err)
	}
}

func (f *vpcCreationFixture) tencentElasticIPRelease(w http.ResponseWriter, body []byte, action string, send func(any)) bool {
	switch action {
	case "ReleaseAddresses":
		var data struct{ AddressIds []string }
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || !slices.Equal(data.AddressIds, []string{f.second}) {
			return false
		}
		f.releases.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	case "DescribeAddresses":
		var data struct {
			Offset, Limit int
			AddressIds    []string
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || !slices.Equal(data.AddressIds, []string{f.second}) || data.Offset != 0 || data.Limit != 2 {
			return false
		}
		rows := []any{}
		if f.visible.Load() {
			row := map[string]any{"AddressId": f.second, "AddressIp": f.releaseIP(), "AddressType": "EIP",
				"AddressStatus": "UNBIND", "InternetChargeType": fixtureTencentEIPCharge, "IsArrears": false, "IsBlocked": f.protectedIP.Load()}
			if f.blockedGroup.Load() {
				row["AddressStatus"], row["InstanceId"] = "BIND", "ins-bound"
			}
			rows = append(rows, row)
		}
		if f.invalidAbsence.Load() {
			send(map[string]any{"AddressSet": rows, "TotalCount": 1})
			return true
		}
		send(map[string]any{"AddressSet": rows, "TotalCount": len(rows)})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaElasticIPRelease(w http.ResponseWriter, r *http.Request, action string, send func(any)) bool {
	query := r.URL.Query()
	if r.Host != "vpc.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2016-04-28" ||
		query.Get("RegionId") != f.region || query.Get("AllocationId") != f.second {
		return false
	}
	switch action {
	case "ReleaseEipAddress":
		if len(query) != 2 {
			return false
		}
		f.releases.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	case "DescribeEipAddresses":
		if len(query) != 4 || query.Get("PageNumber") != "1" || query.Get("PageSize") != "10" {
			return false
		}
		rows := []any{}
		if f.visible.Load() {
			row := map[string]any{"AllocationId": f.second, "IpAddress": f.releaseIP(), "Status": "Available",
				"RegionId": f.region, "ChargeType": "PostPaid", "BusinessStatus": "Normal", "ServiceManaged": 0,
				"DeletionProtection": f.protectedIP.Load()}
			if f.blockedGroup.Load() {
				row["Status"], row["InstanceId"] = "InUse", "i-bound"
			}
			rows = append(rows, row)
		}
		count := len(rows)
		if f.invalidAbsence.Load() {
			count = 1
		}
		send(map[string]any{"TotalCount": count, "PageNumber": 1, "PageSize": 10, "EipAddresses": map[string]any{"EipAddress": rows}})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiElasticIPRelease(w http.ResponseWriter, r *http.Request, send func(any)) bool {
	path := "/v3/" + fixtureHuaweiProject + "/eip/publicips"
	if r.Method == http.MethodDelete && r.URL.Path == path+"/"+f.second && r.URL.RawQuery == "" {
		f.releases.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	query := r.URL.Query()
	if r.Method != http.MethodGet || r.URL.Path != path || query.Get("id") != f.second || len(query["id"]) != 1 ||
		query.Get("limit") != "2000" || !strings.Contains(r.URL.RawQuery, "fields=project_id") {
		return false
	}
	rows := []any{}
	if f.visible.Load() {
		row := map[string]any{"id": f.second, "project_id": fixtureHuaweiProject, "public_ip_address": f.releaseIP(), "type": "EIP",
			"ip_version": 4, "status": "DOWN", "bandwidth": map[string]string{"share_type": "PER"}}
		if f.blockedGroup.Load() {
			row["status"], row["associate_instance_id"] = "ACTIVE", f.first
		}
		if f.protectedIP.Load() {
			row["lock_status"] = "locked"
		}
		rows = append(rows, row)
	}
	count := len(rows)
	if f.invalidAbsence.Load() {
		count = 1
	}
	send(map[string]any{"publicips": rows, "page_info": map[string]int{"current_count": count}})
	return true
}

func (f *vpcCreationFixture) releaseIP() string {
	if f.badEvidence.Load() {
		return fixtureAllocatedFirstIP
	}
	return fixtureAllocatedSecondIP
}
