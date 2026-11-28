package apitest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	operationhttp "with-cloud-manager/app/handlers/operations"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceBatchOperations(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) },
		func() error { return operations.Migrate(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
	    INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','operator','service','fixture','operator'),('tenant','reader','service','fixture','reader');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','project','operator','operator'),('tenant','project','reader','viewer'),('tenant','other','reader','viewer');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode) VALUES
	    ('tenant','aws','project','aws-ec2','fixture/aws','aws:fixture','write_enabled'),
	    ('tenant','foreign','other','openstack','fixture/foreign','site:fixture','write_enabled');
	    INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
	    VALUES ('tenant','aws','us-east-1','instance',1,now()),('tenant','aws','us-east-1','volume',1,now()),
	    ('tenant','foreign','us-east-1','instance',1,now());
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
	    SELECT scope_id,asset,asset,'available',1 FROM cmp_inventory_scopes AS s
	    JOIN (VALUES ('aws','instance','a'),('aws','instance','b'),('aws','instance','c'),('aws','instance','d'),
	    ('aws','instance','e'),('aws','instance','f'),('aws','instance','g'),('aws','volume','vol-one'),('aws','volume','vol-two'),
	    ('foreign','instance','private')) AS v(account_id,kind,asset) USING (account_id,kind);
	    INSERT INTO cmp_protected_instances (scope_id,external_id)
	    SELECT scope_id,external_id FROM cmp_inventory_objects WHERE external_id='g';`)
	const operator, reader = "fixture-batch-operator-service-token", "fixture-batch-reader-service-token"
	for principal, secret := range map[string]string{"operator": operator, "reader": reader} {
		digest := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')",
			digest[:], principal)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const root = "/v1/tenants/tenant/projects/project"
	const batchPath = root + "/operation-batches"
	request := func(method, path, secret, key, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
		t.Helper()
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d want=%d response=%s", w.Code, status, w.Body)
		}
		return w
	}
	encode := func(items ...operations.BatchItem) string {
		t.Helper()
		data, err := json.Marshal(operations.BatchInput{Items: items})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	makeItem := func(id, action string) operations.BatchItem {
		return operations.BatchItem{Input: operations.Input{Account: "aws", Region: "us-east-1", AssetID: id, Action: action}, Revision: 1}
	}
	runPreview := func(payload string) operations.BatchPreview {
		t.Helper()
		w := expect(request(http.MethodPost, batchPath+"/preview", operator, "", payload), http.StatusOK)
		var response struct{ Data operations.BatchPreview }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data.Items == nil {
			t.Fatalf("invalid preview %s error=%v", w.Body, err)
		}
		return response.Data
	}
	decodeReceipt := func(w *httptest.ResponseRecorder, code int) operations.BatchReceipt {
		t.Helper()
		expect(w, code)
		var response struct{ Data operations.BatchReceipt }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data.Operations) == 0 || response.Data.ID == "" {
			t.Fatalf("invalid receipt %s error=%v", w.Body, err)
		}
		return response.Data
	}
	count := func(batches, ops, queued, events int) {
		t.Helper()
		var b, o, q, e int
		err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM cmp_operation_batches),(SELECT count(*) FROM cmp_instance_operations),
		    (SELECT count(*) FROM cmp_operation_tasks),(SELECT count(*) FROM cmp_instance_operation_events)`).Scan(&b, &o, &q, &e)
		if err != nil || b != batches || o != ops || q != queued || e != events {
			t.Fatalf("batches=%d operations=%d queued=%d events=%d error=%v", b, o, q, e, err)
		}
	}
	a, b, c := makeItem("a", "delete"), makeItem("b", "start"), makeItem("c", "stop")
	ready := encode(b, a)
	stale := c
	stale.Revision = 99
	protected, foreign := makeItem("g", "delete"), makeItem("private", "stop")
	foreign.Account = "foreign"
	preview := runPreview(encode(a, stale, protected, foreign))
	if preview.Remaining != 100 || preview.CanSubmit || len(preview.Items) != 4 || preview.Items[0].Reason != "ready" ||
		!preview.Items[0].ApprovalRequired || preview.Items[1].Reason != "stale" || preview.Items[1].OwnershipRevision != 1 ||
		preview.Items[2].Reason != "protected" || preview.Items[3].Reason != "unavailable" || preview.Items[3].OwnershipRevision != 0 {
		t.Fatalf("incorrect preview: %+v", preview)
	}
	count(0, 0, 0, 0)
	expect(request(http.MethodPost, batchPath+"/preview", reader, "", ready), http.StatusForbidden)
	expect(request(http.MethodPost, batchPath+"/preview", "", "", ready), http.StatusUnauthorized)
	expect(request(http.MethodPost, batchPath+"/preview", operator, "", encode(a, a)), http.StatusBadRequest)
	expect(request(http.MethodPost, batchPath+"/preview", operator, "", strings.Repeat(" ", operationhttp.BatchBodyLimit)+ready), http.StatusRequestEntityTooLarge)
	if w := expect(request(http.MethodPost, batchPath, operator, "stale", encode(b, stale)), http.StatusPreconditionFailed); !strings.Contains(w.Body.String(), `"index":1`) {
		t.Fatalf("stale result omitted index: %s", w.Body)
	}
	expect(request(http.MethodPost, batchPath, operator, "protected", encode(b, protected)), http.StatusConflict)
	exec("INSERT INTO cmp_instance_operation_budgets (tenant_id,project_id,max_active) VALUES ('tenant','project',1)")
	if short := runPreview(ready); short.CanSubmit || short.Remaining != 1 {
		t.Fatalf("invalid batch quota preview: %+v", short)
	}
	expect(request(http.MethodPost, batchPath, operator, "quota", ready), http.StatusTooManyRequests)
	exec("UPDATE cmp_instance_operation_budgets SET max_active=10")
	exec("ALTER TABLE cmp_instance_operation_events ADD CONSTRAINT fail_second_batch_item CHECK (status<>'awaiting_approval') NOT VALID")
	expect(request(http.MethodPost, batchPath, operator, "once", ready), http.StatusServiceUnavailable)
	count(0, 0, 0, 0)
	exec("ALTER TABLE cmp_instance_operation_events DROP CONSTRAINT fail_second_batch_item")

	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			results <- request(http.MethodPost, batchPath, operator, "once", ready)
		}()
	}
	close(start)
	first, second := <-results, <-results
	accepted := decodeReceipt(first, http.StatusAccepted)
	replayed := decodeReceipt(second, http.StatusAccepted)
	location := first.Header().Get("Location")
	if accepted.ID != replayed.ID || len(accepted.Operations) != 2 || len(replayed.Operations) != 2 ||
		accepted.Operations[0].ID != replayed.Operations[0].ID || accepted.Operations[1].ID != replayed.Operations[1].ID ||
		accepted.Operations[0].AssetID != "b" || accepted.Operations[0].Status != "queued" ||
		accepted.Operations[1].AssetID != "a" || accepted.Operations[1].Status != "awaiting_approval" ||
		location != batchPath+"/"+accepted.ID || location != second.Header().Get("Location") {
		t.Fatalf("order or replay mismatch: %+v / %+v", accepted, replayed)
	}
	if read := decodeReceipt(request(http.MethodGet, location, reader, "", ""), http.StatusOK); read.Operations[1].ID != accepted.Operations[1].ID {
		t.Fatal("batch linkage lost")
	}
	expect(request(http.MethodGet, strings.Replace(location, "/project/", "/other/", 1), reader, "", ""), http.StatusForbidden)
	expect(request(http.MethodPost, batchPath, operator, "once", encode(a, b)), http.StatusConflict)
	expect(request(http.MethodPost, batchPath, operator, "busy", encode(c, b)), http.StatusConflict)
	if busy := runPreview(encode(c, b)); busy.CanSubmit || busy.Items[1].Reason != "busy" {
		t.Fatalf("active same instance accepted: %+v", busy)
	}
	count(1, 2, 1, 2)

	d := makeItem("d", "attach-volume")
	d.VolumeID, d.VolumeRevision, d.Device = "vol-one", 99, "/dev/sdf"
	if volume := runPreview(encode(d)); volume.Items[0].Reason != "volume_stale" || volume.Items[0].VolumeRevision != 1 {
		t.Fatalf("volume revision conflict lost: %+v", volume)
	}
	d.VolumeRevision = 1
	attach := c
	attach.Action, attach.VolumeID, attach.VolumeRevision, attach.Device = "attach-volume", "vol-one", 1, "/dev/sdf"
	expect(request(http.MethodPost, batchPath+"/preview", operator, "", encode(d, attach)), http.StatusBadRequest)
	attach.VolumeID = "vol-two"
	server := httptest.NewServer(app)
	defer server.Close()
	payload := encode(d, attach)
	var output bytes.Buffer
	if err := commands.RunOperationsClient(ctx, []string{"preview-batch", "tenant", "project"}, server.URL, operator, strings.NewReader(payload), &output, nil); err != nil ||
		!strings.Contains(output.String(), `"can_submit":true`) {
		t.Fatalf("invalid batch CLI preview: %s error=%v", output.String(), err)
	}
	output.Reset()
	if err := commands.RunOperationsClient(ctx, []string{"submit-batch", "tenant", "project", "--idempotency-key", "volumes"}, server.URL, operator,
		strings.NewReader(payload), &output, nil); err != nil {
		t.Fatal(err)
	}
	var volumeBatch operations.BatchReceipt
	if err := json.Unmarshal(output.Bytes(), &volumeBatch); err != nil || len(volumeBatch.Operations) != 2 || volumeBatch.Operations[1].VolumeID != "vol-two" {
		t.Fatalf("CLI volume batch failed: %s error=%v", output.String(), err)
	}
	output.Reset()
	if err := commands.RunOperationsClient(ctx, []string{"get-batch", "tenant", "project", volumeBatch.ID}, server.URL, reader, nil, &output, nil); err != nil ||
		!strings.Contains(output.String(), volumeBatch.Operations[0].ID) {
		t.Fatalf("CLI batch lookup failed: %s error=%v", output.String(), err)
	}
	sharedVolume := makeItem("e", "attach-volume")
	sharedVolume.VolumeID, sharedVolume.VolumeRevision, sharedVolume.Device = "vol-two", 1, "/dev/sdf"
	if active := runPreview(encode(sharedVolume)); active.CanSubmit || active.Items[0].Reason != "busy" {
		t.Fatalf("active same volume accepted: %+v", active)
	}

	e, f := makeItem("e", "stop"), makeItem("f", "stop")
	start = make(chan struct{})
	for key, payload := range map[string]string{"overlap-first": encode(e, f), "overlap-second": encode(f, e)} {
		go func() {
			<-start
			results <- request(http.MethodPost, batchPath, operator, key, payload)
		}()
	}
	close(start)
	left, right := <-results, <-results
	if left.Code != http.StatusAccepted {
		left, right = right, left
	}
	expect(left, http.StatusAccepted)
	expect(right, http.StatusConflict)
	count(3, 6, 5, 6)
}
