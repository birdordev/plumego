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

func TestNeutronNetworkCreation(t *testing.T) {
	const token, project = "FixtureNetworkToken233", "test-project"
	const networkID, subnetID = "00000000-0000-0000-0000-000000000011", "00000000-0000-0000-0000-000000000022"
	const cidr = "10.24.0.0/24"
	const marker = neutronIntentPrefix + token
	var mode atomic.Int32
	var networkWrites, subnetWrites atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-identity" {
			t.Error("Neutron request without scoped token")
		}
		netOwner := project
		if mode.Load() == 1 {
			netOwner = "foreign-project"
		}
		network := fmt.Sprintf(`{"id":%q,"project_id":%q,"name":"fixture","status":"ACTIVE","shared":false,"description":%q}`, networkID, netOwner, marker)
		subnet := fmt.Sprintf(`{"id":%q,"project_id":%q,"name":"fixture","network_id":%q,"cidr":%q,"ip_version":4,"description":%q}`, subnetID, project, networkID, cidr, marker)
		if r.Method == http.MethodPost {
			var request map[string]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			switch r.URL.Path {
			case "/network/v2.0/networks":
				networkWrites.Add(1)
				if body := request["network"]; body["name"] != "fixture" || body["description"] != marker || body["shared"] != false || body["admin_state_up"] != true {
					t.Error("expected private Neutron network with creation marker")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case "/network/v2.0/subnets":
				subnetWrites.Add(1)
				if body := request["subnet"]; body["name"] != "fixture" || body["description"] != marker ||
					body["network_id"] != networkID || body["cidr"] != cidr || body["ip_version"] != float64(4) {
					t.Error("unexpected Neutron subnet request")
				}
				w.Header().Set("X-Openstack-Request-Id", "req-subnet")
				w.WriteHeader(http.StatusCreated)
				respond(w, `{"subnet":`+subnet+`}`, t)
				return
			}
		}
		switch r.URL.Path {
		case "/network/v2.0/extensions/" + neutronDescriptionExtension:
			if mode.Load() == 2 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			respond(w, `{"extension":{"alias":`+fmt.Sprintf("%q", neutronDescriptionExtension)+`}}`, t)
		case "/network/v2.0/networks/" + networkID:
			respond(w, `{"network":`+network+`}`, t)
		case "/network/v2.0/subnets/" + subnetID:
			respond(w, `{"subnet":`+subnet+`}`, t)
		case "/network/v2.0/networks":
			if q := r.URL.Query(); q.Get("project_id") != project || q.Get("description") != marker || q.Get("limit") != "2" {
				t.Error("unscoped network search")
			}
			if mode.Load() == 3 {
				respond(w, `{"networks":[`+network+`],"networks_links":[{"rel":"next"}]}`, t)
				return
			}
			respond(w, `{"networks":[`+network+`]}`, t)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: "RegionOne", Kind: provider.Network}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-identity", endpoints: map[string]map[string]*url.URL{
		scope.Region: {"network": origin.JoinPath("network")},
	}}
	ctx := t.Context()
	network := provider.NetworkSpec{Name: "fixture"}
	if _, err := client.CreateNetwork(ctx, scope, network, token); !provider.IsFailure(err, provider.CreationBlocked) || networkWrites.Load() != 0 {
		t.Fatalf("writes enabled by default: %v", err)
	}
	client.allowNetworkCreation = true
	mode.Store(2)
	if _, err := client.CreateNetwork(ctx, scope, network, token); !provider.IsFailure(err, provider.CreationBlocked) || networkWrites.Load() != 0 {
		t.Fatalf("write with missing description extension: %v", err)
	}
	mode.Store(0)
	if _, err := client.CreateNetwork(ctx, scope, network, token); err == nil || networkWrites.Load() != 1 {
		t.Fatalf("ambiguous Neutron write count=%d error=%v", networkWrites.Load(), err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, network, token, ""); err != nil || obj.ID != networkID || obj.Status != "ACTIVE" {
		t.Fatalf("invalid network proof: %+v %v", obj, err)
	}
	mode.Store(3)
	if _, err := client.FindCreatedNetwork(ctx, scope, network, token, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("paginated network proof accepted: %v", err)
	}
	mode.Store(1)
	if _, err := client.FindCreatedNetwork(ctx, scope, network, token, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("foreign network accepted: %v", err)
	}
	scope.Kind = provider.Subnet
	subnet := provider.NetworkSpec{Name: "fixture", ParentID: networkID, CIDR: cidr}
	if _, err := client.CreateNetwork(ctx, scope, subnet, token); !provider.IsFailure(err, provider.CreationBlocked) || subnetWrites.Load() != 0 {
		t.Fatalf("foreign parent targeted: %v", err)
	}
	mode.Store(0)
	receipt, err := client.CreateNetwork(ctx, scope, subnet, token)
	if err != nil || receipt.RequestID != "req-subnet" || receipt.ResourceID != subnetID || subnetWrites.Load() != 1 {
		t.Fatalf("invalid subnet receipt: %+v %v", receipt, err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, subnet, token, receipt.ResourceID); err != nil ||
		obj.ID != subnetID || obj.Status != "available" || len(obj.Links) != 1 || obj.Links[0].ID != networkID {
		t.Fatalf("invalid subnet proof: %+v %v", obj, err)
	}
}
