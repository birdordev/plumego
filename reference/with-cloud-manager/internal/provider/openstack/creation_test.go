package openstack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestNovaCreationCorrelation(t *testing.T) {
	const token = "TestNovaCreationToken2031"
	const mark = "cmp-" + token
	const serverID = "12345678-1234-1234-1234-1234567890ab"
	const project = "test-project"
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: "RegionOne", Kind: provider.Instance}
	spec := provider.InstanceSpec{Name: "worker", ImageID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Size: "m1-4",
		NetworkID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", SecurityGroup: "cccccccc-cccc-cccc-cccc-cccccccccccc"}
	var state atomic.Value
	state.Store("")
	var sends, tagReads atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /compute/v2.1/servers", func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		var request novaCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || r.Header.Get("OpenStack-API-Version") != novaLaunchVersion ||
			r.Header.Get("X-Auth-Token") != "fixture-identity" || r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("invalid Nova request: %v", err)
			return
		}
		body := request.Server
		if body.Name != spec.Name || body.Image != spec.ImageID || body.Flavor != spec.Size ||
			len(body.Networks) != 1 || body.Networks[0].UUID != spec.NetworkID ||
			len(body.Groups) != 1 || body.Groups[0].Name != spec.SecurityGroup || len(body.Tags) != 1 || body.Tags[0] != mark {
			t.Error("unexpected Nova create body")
		}
		if state.Load() == "disconnected" {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		w.Header().Set("X-Openstack-Request-Id", "req-created")
		w.WriteHeader(http.StatusAccepted)
		respond(w, fmt.Sprintf(`{"server":{"id":%q}}`, serverID), t)
	})
	mux.HandleFunc("GET /compute/v2.1/servers", func(w http.ResponseWriter, r *http.Request) {
		tagReads.Add(1)
		if q := r.URL.Query(); q.Get("tags") != mark || q.Get("limit") != "2" || q.Has("all_tenants") {
			t.Error("unscoped Nova search")
		}
		switch state.Load() {
		case "missing":
			respond(w, `{"servers":[]}`, t)
		case "ambiguous":
			respond(w, fmt.Sprintf(`{"servers":[{"id":%q},{"id":%q}]}`, serverID, serverID), t)
		case "paginated":
			respond(w, fmt.Sprintf(`{"servers":[{"id":%q}],"servers_links":[{"rel":"next"}]}`, serverID), t)
		default:
			respond(w, fmt.Sprintf(`{"servers":[{"id":%q}]}`, serverID), t)
		}
	})
	mux.HandleFunc("GET /compute/v2.1/servers/"+serverID, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenStack-API-Version") != novaLaunchProofVersion || r.Header.Get("X-Auth-Token") != "fixture-identity" {
			t.Error("invalid Nova proof request")
		}
		owner, flavor, tag, status := project, spec.Size, mark, "ACTIVE"
		switch state.Load() {
		case "foreign":
			owner = "foreign-project"
		case "wrong-flavor":
			flavor = "wrong-flavor"
		case "wrong-tag":
			tag = "other-token"
		case "build":
			status = "BUILD"
		case "error":
			status = "ERROR"
		}
		respond(w, fmt.Sprintf(`{"server":{"id":%q,"tenant_id":%q,"name":"worker","status":%q,
			"image":{"id":%q},"flavor":{"id":%q},"tags":[%q,"env=test"]}}`, serverID, owner, status, spec.ImageID, flavor, tag), t)
	})
	mux.HandleFunc("GET /network/v2.0/ports", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("device_id") != serverID || q.Get("project_id") != project {
			t.Error("wrong Neutron proof scope")
		}
		group, network := spec.SecurityGroup, spec.NetworkID
		switch state.Load() {
		case "port-pending":
			respond(w, `{"ports":[]}`, t)
			return
		case "port-network":
			network = "foreign-network"
		case "port-group":
			group = "foreign-group"
		}
		respond(w, fmt.Sprintf(`{"ports":[{"project_id":%q,"device_id":%q,"device_owner":"compute:nova","network_id":%q,"security_groups":[%q]}]}`,
			project, serverID, network, group), t)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-identity", endpoints: map[string]map[string]*url.URL{
		scope.Region: {"compute": origin.JoinPath("compute", "v2.1"), "network": origin.JoinPath("network")},
	}}
	if _, err := client.CreateInstance(t.Context(), scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || sends.Load() != 0 {
		t.Fatalf("Nova write enabled by default: %v", err)
	}
	client.instanceCreateSizes = []string{spec.Size}
	receipt, err := client.CreateInstance(t.Context(), scope, spec, token)
	if err != nil || receipt.ResourceID != serverID || receipt.RequestID != "req-created" {
		t.Fatalf("bad Nova receipt: %+v error=%v", receipt, err)
	}
	item, err := client.FindCreatedInstance(t.Context(), scope, spec, token, receipt.ResourceID)
	if err != nil || item.ID != serverID || item.Status != "ACTIVE" || len(item.Links) != 3 || len(item.Tags) != 1 || tagReads.Load() != 0 {
		t.Fatalf("invalid UUID observation: %+v error=%v", item, err)
	}
	state.Store("disconnected")
	if _, err := client.CreateInstance(t.Context(), scope, spec, token); err == nil || sends.Load() != 2 {
		t.Fatalf("create lost response sends=%d error=%v", sends.Load(), err)
	}
	if item, err := client.FindCreatedInstance(t.Context(), scope, spec, token, ""); err != nil || item.ID != serverID || tagReads.Load() != 1 {
		t.Fatalf("tag recovery item=%+v error=%v", item, err)
	}
	for problem, want := range map[string]provider.FailureKind{
		"missing": provider.OutcomeUnknown, "port-pending": provider.OutcomeUnknown,
		"ambiguous": provider.InvalidResponse, "paginated": provider.InvalidResponse, "foreign": provider.InvalidResponse,
		"wrong-flavor": provider.InvalidResponse, "wrong-tag": provider.InvalidResponse,
		"port-network": provider.InvalidResponse, "port-group": provider.InvalidResponse,
	} {
		t.Run(problem, func(t *testing.T) {
			state.Store(problem)
			if _, err := client.FindCreatedInstance(t.Context(), scope, spec, token, ""); !provider.IsFailure(err, want) {
				t.Fatalf("failed to reject %s: %v", problem, err)
			}
		})
	}
	state.Store("build")
	if item, err := client.FindCreatedInstance(t.Context(), scope, spec, token, serverID); err != nil || item.Status != "BUILD" {
		t.Fatalf("build observation %+v error=%v", item, err)
	}
}
