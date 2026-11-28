package cloudtest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/operations"
)

const fixtureSequence = int64(9007199254740992)

func TestAcceptanceProjectOperationEventStream(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	execFreshness(t, db, `INSERT INTO cmp_instance_operations
		(operation_id,tenant_id,project_id,principal_id,account_id,region,resource_kind,action,status,
		 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
		VALUES ('stream-fixture','lab','compute','viewer','ali','cn-test','network','create-network','applying',
		 'stream-fixture',decode(repeat('00',32),'hex'),1,1,0)`)
	insert := func(offset int64, status string) {
		t.Helper()
		execFreshness(t, db, `INSERT INTO cmp_instance_operation_events (event_id,operation_id,status)
			OVERRIDING SYSTEM VALUE VALUES ($1,'stream-fixture',$2)`, fixtureSequence+offset, status)
	}
	insert(1, "queued")
	insert(2, "applying")
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	server.Client().Timeout = 12 * time.Second
	const path = "/v1/tenants/lab/projects/compute/operations/stream-fixture/events/stream"
	call := func(method, path, token string, headers []string, status int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for _, value := range headers {
			req.Header.Add("Last-Event-ID", value)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		if res.StatusCode != status {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("stream returned %d, want %d: %s", res.StatusCode, status, body)
		}
		return res
	}
	checkEvent := func(read *bufio.Reader, offset int64, status string) {
		t.Helper()
		id, event, data := readOperationStreamFrame(t, read)
		var value operations.OperationEvent
		if err := json.Unmarshal([]byte(data), &value); err != nil || event != "operation" ||
			id != strconv.FormatInt(fixtureSequence+offset, 10) || value.Sequence != fixtureSequence+offset || value.Status != status {
			t.Fatalf("unexpected stream event: id=%s event=%s data=%s err=%v", id, event, data, err)
		}
	}
	call("GET", path, "", nil, 401)
	call("GET", strings.Replace(path, "/compute/", "/shared/", 1), freshnessToken, nil, 403)
	call("GET", path+"?after=1&after=2", freshnessToken, nil, 400)
	call("GET", path+"?after=1;unknown=2", freshnessToken, nil, 400)
	call("GET", path, freshnessToken, []string{"1", "2"}, 400)
	head := call("HEAD", path, freshnessToken, nil, 200)
	if head.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("HEAD did not describe the stream")
	}
	const firstPage = "?after=9007199254740993"
	first := call("GET", path+firstPage, freshnessToken, nil, 200)
	if first.Header.Get("Cache-Control") != "no-store" || first.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("wrong stream buffering policy: %v", first.Header)
	}
	checkEvent(bufio.NewReader(first.Body), 2, "applying")
	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}

	insert(3, "observing")
	reconnected := call("GET", path+firstPage, freshnessToken, []string{"9007199254740994"}, 200)
	reader := bufio.NewReader(reconnected.Body)
	checkEvent(reader, 3, "observing")
	insert(4, "needs_review")
	checkEvent(reader, 4, "needs_review")
	execFreshness(t, db, `UPDATE cmp_memberships SET active=false
		WHERE (tenant_id,project_id,principal_id)=('lab','compute','viewer')`)
	insert(5, "succeeded")
	if _, event, _ := readOperationStreamFrame(t, reader); event != "access-revoked" {
		t.Fatalf("stream delivered data after permission revocation: %s", event)
	}
	call("GET", path, freshnessToken, nil, 403)

	execFreshness(t, db, `UPDATE cmp_memberships SET active=true
		WHERE (tenant_id,project_id,principal_id)=('lab','compute','viewer')`)
	afterLast := path + fmt.Sprintf("?after=%d", fixtureSequence+5)
	connections := make([]*http.Response, 0, 32)
	for range cap(connections) {
		connections = append(connections, call("GET", afterLast, freshnessToken, nil, 200))
	}
	call("GET", afterLast, freshnessToken, nil, 503)
	for _, res := range connections {
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// 等待取消传播并确认连接额度可以重新使用。
	wait := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequestWithContext(t.Context(), "HEAD", server.URL+afterLast, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+freshnessToken)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode == 200 {
			break
		}
		if res.StatusCode != 503 || time.Now().After(wait) {
			t.Fatalf("stream capacity did not recover: %d", res.StatusCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
	call("GET", afterLast, freshnessToken, nil, 200)
	drain, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := server.Config.Shutdown(drain); err != nil {
		t.Fatalf("open event stream prevented graceful shutdown: %v", err)
	}
}

func readOperationStreamFrame(t *testing.T, reader *bufio.Reader) (id, event, data string) {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(line, "\n")
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			id = value
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			event = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			data = value
		}
		if line == "" && event != "" {
			return id, event, data
		}
	}
}
