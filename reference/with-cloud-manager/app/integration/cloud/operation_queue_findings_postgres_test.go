package cloudtest

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceOperationQueueFindings(t *testing.T) {
	db := dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	const token = "fixture-operation-queue-findings-viewer"
	seedOperationBacklog(t, db, token)
	seedQueueAlertEvidence(t, db, "fixture-operation-queue-findings-admin")
	pitrExec(t, db, `UPDATE cmp_operation_tasks SET status='done' WHERE id='ready';
		UPDATE cmp_operation_tasks SET topic='other' WHERE id='ready-two';
		UPDATE cmp_operation_tasks SET lease_until=now()-interval '1 minute' WHERE id='recovery'`)
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/tenants/lab/projects/compute/operations"
	read := func(target string) operations.OperationPage {
		t.Helper()
		w := freshnessRequest(t, app, target, token)
		requireFreshnessStatus(t, w, http.StatusOK)
		var page operations.OperationPage
		testsupport.DecodeProjectData(t, w, &page)
		for _, value := range []string{"secret-owner", "private-payload", "reference-secret", "hidden-operation"} {
			if strings.Contains(w.Body.String(), value) {
				t.Fatal("queue findings exposed internal or out of scope data")
			}
		}
		return page
	}
	first := read(path + "?queue_issue=anomaly&limit=2")
	if len(first.Items) != 2 || first.Next == "" {
		t.Fatalf("first queue findings page: %+v", first)
	}
	for _, conflict := range []string{"queue_issue=overdue_recovery", "queue_issue=anomaly&account=tc", "status=queued"} {
		requireFreshnessStatus(t, freshnessRequest(t, app, path+"?"+conflict+"&cursor="+url.QueryEscape(first.Next), token), http.StatusBadRequest)
	}
	results := make(map[string]string)
	for page, n := first, 0; ; n++ {
		for _, operation := range page.Items {
			if _, found := results[operation.ID]; found {
				t.Fatalf("duplicate queue finding %s", operation.ID)
			}
			results[operation.ID] = operation.QueueIssueReason
		}
		if page.Next == "" {
			break
		}
		if n == 2 {
			t.Fatal("unexpected number of queue findings pages")
		}
		page = read(path + "?queue_issue=anomaly&limit=2&cursor=" + url.QueryEscape(page.Next))
	}
	want := map[string]string{"ready": "finished_task", "ready-two": "missing_task", "missing": "missing_task",
		"foreign-task": "missing_task", "expires": "expired_task", "dead": "dead_letter"}
	if !maps.Equal(want, results) {
		t.Fatalf("queue anomaly reasons: %v, want %v", results, want)
	}
	filtered := read(path + "?queue_issue=anomaly&account=tc&region=ap-test&status=queued")
	if len(filtered.Items) != 1 || filtered.Items[0].ID != "ready" {
		t.Fatalf("findings ignored operation filters: %+v", filtered)
	}
	for _, query := range []string{"queue_issue=", "queue_issue=ready", "queue_issue=anomaly&queue_issue=overdue_recovery"} {
		requireFreshnessStatus(t, freshnessRequest(t, app, path+"?"+query, token), http.StatusBadRequest)
	}
	for _, outOfScope := range []string{"/v1/tenants/foreign/projects/compute/operations", "/v1/tenants/lab/projects/hidden/operations"} {
		requireFreshnessStatus(t, freshnessRequest(t, app, outOfScope+"?queue_issue=anomaly", token), http.StatusForbidden)
	}
	if page := read("/v1/tenants/lab/projects/empty/operations?queue_issue=anomaly"); len(page.Items) != 0 || page.Next != "" {
		t.Fatalf("empty project leaked queue findings: %+v", page)
	}
	plain := read(path)
	if len(plain.Items) <= len(results) || plain.Items[0].QueueIssueReason != "" {
		t.Fatalf("unfiltered operation list changed shape: %+v", plain)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var output bytes.Buffer
	if err := commands.RunOperationsClient(t.Context(), []string{"list", "lab", "compute", "--queue-issue", "overdue_recovery"},
		server.URL, token, nil, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var page operations.OperationPage
	if err := json.Unmarshal(output.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("invalid overdue findings via CLI: %s, %v", output.String(), err)
	}
	for _, item := range page.Items {
		if (item.ID != "tc-overdue" && item.ID != "tc-overdue-east") || item.QueueIssueReason != "lease_recovery_overdue" {
			t.Fatalf("incorrect overdue finding %+v", item)
		}
	}
	var spec openapi.Document
	if err := json.Unmarshal(freshnessRequest(t, app, "/openapi.json", token).Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	operation := spec.Paths["/v1/tenants/{tenant}/projects/{project}/operations"].Get
	if operation == nil {
		t.Fatal("project operation list missing from strict OpenAPI")
	}
	for _, param := range operation.Parameters {
		if param.Name == "queue_issue" && !param.Required && strings.Contains(param.Description, "overdue_recovery") {
			return
		}
	}
	t.Fatal("queue issue filter missing from strict OpenAPI")
}
