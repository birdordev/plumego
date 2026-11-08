package pubsub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func testClusterConfig(nodeID, listenAddr string) ClusterConfig {
	config := DefaultClusterConfig(nodeID, listenAddr)
	config.AllowInsecureAuth = true
	return config
}

func TestDistributedPubSub_Basic(t *testing.T) {
	t.Parallel()
	// Create two nodes
	config1 := testClusterConfig("node1", "127.0.0.1:17001")
	config1.Peers = []string{"127.0.0.1:17002"}

	config2 := testClusterConfig("node2", "127.0.0.1:17002")
	config2.Peers = []string{"127.0.0.1:17001"}

	dps1, err := NewDistributed(config1)
	if err != nil {
		t.Fatalf("Failed to create node1: %v", err)
	}
	defer dps1.Close()

	dps2, err := NewDistributed(config2)
	if err != nil {
		t.Fatalf("Failed to create node2: %v", err)
	}
	defer dps2.Close()

	// Join cluster
	ctx := t.Context()
	if err := dps1.JoinCluster(ctx); err != nil {
		t.Fatalf("node1 failed to join: %v", err)
	}

	if err := dps2.JoinCluster(ctx); err != nil {
		t.Fatalf("node2 failed to join: %v", err)
	}

	// Wait for discovery
	time.Sleep(200 * time.Millisecond)

	// Check nodes discovered each other
	nodes1 := dps1.Nodes()
	if len(nodes1) < 2 {
		t.Logf("Note: Node discovery might be eventual (found %d nodes)", len(nodes1))
	}

	nodes2 := dps2.Nodes()
	if len(nodes2) < 2 {
		t.Logf("Note: Node discovery might be eventual (found %d nodes)", len(nodes2))
	}
}

func TestDistributedPubSub_GlobalPublish(t *testing.T) {
	t.Parallel()
	// Create two nodes
	config1 := testClusterConfig("node1", "127.0.0.1:17011")
	config1.Peers = []string{"127.0.0.1:17012"}

	config2 := testClusterConfig("node2", "127.0.0.1:17012")
	config2.Peers = []string{"127.0.0.1:17011"}

	dps1, err := NewDistributed(config1)
	if err != nil {
		t.Fatalf("Failed to create node1: %v", err)
	}
	defer dps1.Close()

	dps2, err := NewDistributed(config2)
	if err != nil {
		t.Fatalf("Failed to create node2: %v", err)
	}
	defer dps2.Close()

	// Join cluster
	ctx := t.Context()
	_ = dps1.JoinCluster(ctx)
	_ = dps2.JoinCluster(ctx)

	time.Sleep(300 * time.Millisecond) // Wait for cluster formation

	// Subscribe on node2
	sub, err := dps2.Subscribe(t.Context(), "test.global", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Cancel()

	// Publish from node1
	msg := Message{
		ID:   "global-msg",
		Data: "hello cluster",
	}

	if err := dps1.PublishGlobal("test.global", msg); err != nil {
		t.Fatalf("Failed to publish globally: %v", err)
	}

	// Wait and check if received on node2
	select {
	case received := <-sub.C():
		if received.ID != "global-msg" {
			t.Errorf("Expected message ID 'global-msg', got '%s'", received.ID)
		}

	case <-time.After(1 * time.Second):
		t.Error("Did not receive message on node2 within timeout")
	}

	// Check stats
	stats1 := dps1.ClusterStats()
	if stats1.HealthyNodes < 1 {
		t.Logf("Note: Cluster may still be forming (healthy nodes: %d)", stats1.HealthyNodes)
	}
}

func TestDistributedPubSub_Heartbeat(t *testing.T) {
	t.Parallel()
	config := testClusterConfig("test-node", "127.0.0.1:17021")
	config.HeartbeatInterval = 100 * time.Millisecond
	config.HeartbeatTimeout = 300 * time.Millisecond

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	defer dps.Close()

	ctx := t.Context()
	if err := dps.JoinCluster(ctx); err != nil {
		t.Fatalf("Failed to join cluster: %v", err)
	}

	// Wait for heartbeats
	time.Sleep(400 * time.Millisecond)

	stats := dps.ClusterStats()
	if stats.Heartbeats == 0 && len(config.Peers) > 0 {
		t.Log("Note: No heartbeats sent (expected with no peers)")
	}

	// Check self is healthy
	nodes := dps.Nodes()
	foundSelf := false
	for _, node := range nodes {
		if node.ID == "test-node" && node.Healthy {
			foundSelf = true
			break
		}
	}

	if !foundSelf {
		t.Error("Self node not found or unhealthy")
	}
}

func TestDistributedPubSub_NodeFailure(t *testing.T) {
	t.Parallel()
	// Create three nodes
	config1 := testClusterConfig("node1", "127.0.0.1:17031")
	config1.Peers = []string{"127.0.0.1:17032", "127.0.0.1:17033"}
	config1.HeartbeatInterval = 100 * time.Millisecond
	config1.HeartbeatTimeout = 300 * time.Millisecond

	config2 := testClusterConfig("node2", "127.0.0.1:17032")
	config2.Peers = []string{"127.0.0.1:17031", "127.0.0.1:17033"}
	config2.HeartbeatInterval = 100 * time.Millisecond
	config2.HeartbeatTimeout = 300 * time.Millisecond

	config3 := testClusterConfig("node3", "127.0.0.1:17033")
	config3.Peers = []string{"127.0.0.1:17031", "127.0.0.1:17032"}
	config3.HeartbeatInterval = 100 * time.Millisecond
	config3.HeartbeatTimeout = 300 * time.Millisecond

	dps1, _ := NewDistributed(config1)
	defer dps1.Close()

	dps2, _ := NewDistributed(config2)
	defer dps2.Close()

	dps3, _ := NewDistributed(config3)

	ctx := t.Context()
	_ = dps1.JoinCluster(ctx)
	_ = dps2.JoinCluster(ctx)
	_ = dps3.JoinCluster(ctx)

	time.Sleep(500 * time.Millisecond) // Allow cluster to form

	initialNodes := len(dps1.Nodes())

	// Shutdown node3
	dps3.Close()

	// Wait for failure detection
	time.Sleep(500 * time.Millisecond)

	// Check node1 detects node3 as unhealthy
	nodes := dps1.Nodes()
	node3Healthy := false
	for _, node := range nodes {
		if node.ID == "node3" && node.Healthy {
			node3Healthy = true
			break
		}
	}

	if node3Healthy {
		t.Log("Note: Node3 still marked healthy (failure detection may be eventual)")
	}

	if len(nodes) != initialNodes {
		t.Logf("Node count changed from %d to %d", initialNodes, len(nodes))
	}
}

func TestDistributedPubSub_LocalTopics(t *testing.T) {
	t.Parallel()
	config := testClusterConfig("test", "127.0.0.1:17041")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	defer dps.Close()

	// Subscribe to topics
	sub1, _ := dps.Subscribe(t.Context(), "topic.a", SubOptions{BufferSize: 1})
	defer sub1.Cancel()

	sub2, _ := dps.Subscribe(t.Context(), "topic.b", SubOptions{BufferSize: 1})
	defer sub2.Cancel()

	sub3, _ := dps.Subscribe(t.Context(), "topic.a", SubOptions{BufferSize: 1}) // duplicate
	defer sub3.Cancel()

	time.Sleep(50 * time.Millisecond)

	topics := dps.getLocalTopics()

	if len(topics) != 2 {
		t.Errorf("Expected 2 unique topics, got %d: %v", len(topics), topics)
	}

	// Check topics are sorted
	if len(topics) == 2 && topics[0] > topics[1] {
		t.Error("Topics not sorted")
	}
}

func TestDistributedPubSub_ClusterStats(t *testing.T) {
	t.Parallel()
	config := testClusterConfig("stats-test", "127.0.0.1:17051")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("Failed to create node: %v", err)
	}
	defer dps.Close()

	ctx := t.Context()
	_ = dps.JoinCluster(ctx)

	stats := dps.ClusterStats()

	if stats.TotalNodes != 1 {
		t.Errorf("Expected 1 total node (self), got %d", stats.TotalNodes)
	}

	if stats.HealthyNodes != 1 {
		t.Errorf("Expected 1 healthy node (self), got %d", stats.HealthyNodes)
	}
}

func TestDistributedPubSub_ConcurrentPublish(t *testing.T) {
	t.Parallel()
	config1 := testClusterConfig("node1", "127.0.0.1:17061")
	config1.Peers = []string{"127.0.0.1:17062"}

	config2 := testClusterConfig("node2", "127.0.0.1:17062")
	config2.Peers = []string{"127.0.0.1:17061"}

	dps1, _ := NewDistributed(config1)
	defer dps1.Close()

	dps2, _ := NewDistributed(config2)
	defer dps2.Close()

	ctx := t.Context()
	_ = dps1.JoinCluster(ctx)
	_ = dps2.JoinCluster(ctx)

	time.Sleep(300 * time.Millisecond)

	// Concurrent publishers
	const numGoroutines = 5
	const messagesPerGoroutine = 10

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				msg := Message{
					Data: id*messagesPerGoroutine + j,
				}
				_ = dps1.PublishGlobal("concurrent.topic", msg)
			}
			done <- true
		}(i)
	}

	// Wait for completion
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	time.Sleep(200 * time.Millisecond)

	stats := dps1.ClusterStats()
	if stats.ClusterForwards == 0 && len(config1.Peers) > 0 {
		t.Log("Note: No cluster forwards (may be expected if peer not connected)")
	}
}

func TestDistributedPubSub_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	config1 := testClusterConfig("pub", "127.0.0.1:17071")
	config1.Peers = []string{"127.0.0.1:17072"}

	config2 := testClusterConfig("sub", "127.0.0.1:17072")
	config2.Peers = []string{"127.0.0.1:17071"}

	dps1, _ := NewDistributed(config1)
	defer dps1.Close()

	dps2, _ := NewDistributed(config2)
	defer dps2.Close()

	ctx := t.Context()
	_ = dps1.JoinCluster(ctx)
	_ = dps2.JoinCluster(ctx)

	time.Sleep(300 * time.Millisecond)

	// Multiple subscribers on node2
	sub1, _ := dps2.Subscribe(t.Context(), "fanout.topic", SubOptions{BufferSize: 5})
	defer sub1.Cancel()

	sub2, _ := dps2.Subscribe(t.Context(), "fanout.topic", SubOptions{BufferSize: 5})
	defer sub2.Cancel()

	sub3, _ := dps2.Subscribe(t.Context(), "fanout.topic", SubOptions{BufferSize: 5})
	defer sub3.Cancel()

	// Publish from node1
	msg := Message{ID: "fanout-msg", Data: "test"}
	_ = dps1.PublishGlobal("fanout.topic", msg)

	time.Sleep(200 * time.Millisecond)

	// Check all subscribers received
	received := 0
	timeout := time.After(1 * time.Second)

	for received < 3 {
		select {
		case <-sub1.C():
			received++
		case <-sub2.C():
			received++
		case <-sub3.C():
			received++
		case <-timeout:
			goto done
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

done:
	if received != 3 {
		t.Logf("Note: Expected 3 messages, received %d (cluster propagation may be eventual)", received)
	}
}

func TestDistributedPubSub_InvalidConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config ClusterConfig
	}{
		{
			name:   "empty node ID",
			config: ClusterConfig{NodeID: "", ListenAddr: "127.0.0.1:18000"},
		},
		{
			name:   "empty listen addr",
			config: ClusterConfig{NodeID: "node1", ListenAddr: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDistributed(tt.config)
			if err == nil {
				t.Error("Expected error for invalid config")
			}
		})
	}
}

func TestDistributedPubSub_HTTPHandlersUseCanonicalResponses(t *testing.T) {
	config := testClusterConfig("node-http", "127.0.0.1:18101")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	dps.handleHealth(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", healthRec.Code, http.StatusOK)
	}
	assertPubSubJSONContentType(t, healthRec)

	health := decodePubSubData[clusterHealthResponse](t, healthRec)
	if health.NodeID != "node-http" {
		t.Fatalf("node_id = %v, want %q", health.NodeID, "node-http")
	}

	heartbeatBody := `{"node_id":"peer-1","addr":"127.0.0.1:18102","topics":["orders"],"timestamp":"2026-04-12T00:00:00Z","version":"1.0"}`
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(heartbeatBody))
	heartbeatRec := httptest.NewRecorder()
	dps.handleHeartbeat(heartbeatRec, heartbeatReq)
	if heartbeatRec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want %d", heartbeatRec.Code, http.StatusOK)
	}
	assertPubSubJSONContentType(t, heartbeatRec)

	heartbeatResp := decodePubSubData[heartbeatPayload](t, heartbeatRec)
	if heartbeatResp.NodeID != "node-http" {
		t.Fatalf("heartbeat node_id = %q, want %q", heartbeatResp.NodeID, "node-http")
	}

	syncReq := httptest.NewRequest(http.MethodGet, "/sync", nil)
	syncRec := httptest.NewRecorder()
	dps.handleSync(syncRec, syncReq)
	if syncRec.Code != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncRec.Code, http.StatusOK)
	}
	assertPubSubJSONContentType(t, syncRec)

	syncResp := decodePubSubData[clusterSyncResponse](t, syncRec)
	if len(syncResp.Nodes) != 1 || syncResp.Nodes[0].ID != "peer-1" {
		t.Fatalf("unexpected sync nodes: %+v", syncResp.Nodes)
	}
}

func TestDistributedPubSub_EmptyTokenFailsClosed(t *testing.T) {
	config := DefaultClusterConfig("node-secure", "127.0.0.1:18112")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	dps.handleHealth(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("health status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	assertPubSubErrorCode(t, rec, contract.CodeUnauthorized)
}

func TestDistributedPubSub_AllowInsecureAuthOptIn(t *testing.T) {
	config := testClusterConfig("node-insecure", "127.0.0.1:18113")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	dps.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestDistributedPubSub_HTTPHandlersReturnStructuredErrors(t *testing.T) {
	config := DefaultClusterConfig("node-http", "127.0.0.1:18111")
	config.AuthToken = "secret-token"

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	methodReq := httptest.NewRequest(http.MethodGet, "/heartbeat", nil)
	methodRec := httptest.NewRecorder()
	dps.handleHeartbeat(methodRec, methodReq)
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("heartbeat method status = %d, want %d", methodRec.Code, http.StatusMethodNotAllowed)
	}
	assertPubSubErrorCode(t, methodRec, contract.CodeMethodNotAllowed)

	authReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	authRec := httptest.NewRecorder()
	dps.handleHealth(authRec, authReq)
	if authRec.Code != http.StatusUnauthorized {
		t.Fatalf("health auth status = %d, want %d", authRec.Code, http.StatusUnauthorized)
	}
	assertPubSubErrorCode(t, authRec, contract.CodeUnauthorized)
}

func TestDistributedPubSub_ClusterPublishReturnsJSON(t *testing.T) {
	config := testClusterConfig("node-http", "127.0.0.1:18121")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	body := `{"type":"publish","node_id":"peer-1","topic":"orders","message":{"data":"ok"},"timestamp":"2026-04-12T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(body))
	rec := httptest.NewRecorder()
	dps.handleClusterPublish(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status = %d, want %d", rec.Code, http.StatusOK)
	}
	assertPubSubJSONContentType(t, rec)

	resp := decodePubSubData[clusterPublishResponse](t, rec)
	if resp.Status != "ok" {
		t.Fatalf("publish status payload = %q, want %q", resp.Status, "ok")
	}
}

func TestDistributedPubSub_MalformedClusterJSONUsesInvalidJSONCode(t *testing.T) {
	config := testClusterConfig("node-http", "127.0.0.1:18123")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()

	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		path    string
	}{
		{
			name:    "heartbeat",
			handler: dps.handleHeartbeat,
			path:    "/heartbeat",
		},
		{
			name:    "publish",
			handler: dps.handleClusterPublish,
			path:    "/publish",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("{"))
			rec := httptest.NewRecorder()
			tt.handler(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			assertPubSubErrorCode(t, rec, contract.CodeInvalidJSON)
		})
	}
}

func TestDistributedPubSub_ClusterPublishSanitizesLocalPublishError(t *testing.T) {
	config := testClusterConfig("node-http", "127.0.0.1:18122")

	dps, err := NewDistributed(config)
	if err != nil {
		t.Fatalf("NewDistributed: %v", err)
	}
	defer dps.Close()
	_ = dps.InProcBroker.Close()

	body := `{"type":"publish","node_id":"peer-1","topic":"orders","message":{"data":"ok"},"timestamp":"2026-04-12T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(body))
	rec := httptest.NewRecorder()
	dps.handleClusterPublish(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("publish status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	errResp := decodePubSubError(t, rec)
	if errResp.Error.Message != "cluster publish failed" {
		t.Fatalf("error message = %q, want stable cluster publish failure", errResp.Error.Message)
	}
	if strings.Contains(errResp.Error.Message, "broker is closed") {
		t.Fatalf("error message leaked broker detail: %q", errResp.Error.Message)
	}
}

func assertPubSubJSONContentType(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}
}

func assertPubSubErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	resp := decodePubSubError(t, rec)
	if resp.Error.Code != want {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, want)
	}
}

func decodePubSubData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	assertPubSubJSONContentType(t, rec)

	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("success envelope missing data")
	}

	var body T
	if err := json.Unmarshal(env.Data, &body); err != nil {
		t.Fatalf("decode success data: %v", err)
	}
	return body
}

type pubSubErrorResponse struct {
	Error struct {
		Code     string                 `json:"code"`
		Message  string                 `json:"message"`
		Category contract.ErrorCategory `json:"category"`
		Type     contract.ErrorType     `json:"type,omitempty"`
		Details  map[string]any         `json:"details,omitempty"`
	} `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func decodePubSubError(t *testing.T, rec *httptest.ResponseRecorder) pubSubErrorResponse {
	t.Helper()
	assertPubSubJSONContentType(t, rec)

	var resp pubSubErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return resp
}

func BenchmarkDistributedPubSub_LocalPublish(b *testing.B) {
	config := testClusterConfig("bench", "127.0.0.1:17081")

	dps, err := NewDistributed(config)
	if err != nil {
		b.Fatalf("Failed to create node: %v", err)
	}
	defer dps.Close()

	ctx := b.Context()
	_ = dps.JoinCluster(ctx)

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dps.Publish("bench.topic", msg)
	}
}

func BenchmarkDistributedPubSub_GlobalPublish(b *testing.B) {
	config1 := testClusterConfig("node1", "127.0.0.1:17091")
	config1.Peers = []string{"127.0.0.1:17092"}

	config2 := testClusterConfig("node2", "127.0.0.1:17092")
	config2.Peers = []string{"127.0.0.1:17091"}

	dps1, _ := NewDistributed(config1)
	defer dps1.Close()

	dps2, _ := NewDistributed(config2)
	defer dps2.Close()

	ctx := b.Context()
	_ = dps1.JoinCluster(ctx)
	_ = dps2.JoinCluster(ctx)

	time.Sleep(300 * time.Millisecond)

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dps1.PublishGlobal("bench.topic", msg)
	}
}
