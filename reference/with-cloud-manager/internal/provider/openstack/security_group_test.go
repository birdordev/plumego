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

func TestNeutronSecurityGroupCreation(t *testing.T) {
	const token, project = "FixturePrivateGroupToken22", "test-project"
	const groupID, name = "00000000-0000-0000-0000-000000000033", "fixture group"
	const marker = neutronIntentPrefix + token
	const groups = "/network/v2.0/security-groups"
	var writes atomic.Int32
	var fixture atomic.Value
	fixture.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-identity" {
			t.Error("Neutron request without scoped token")
		}
		mode := fixture.Load().(string)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/network/v2.0/extensions/"+neutronDescriptionExtension:
			respond(w, fmt.Sprintf(`{"extension":{"alias":%q}}`, neutronDescriptionExtension), t)
		case r.Method == http.MethodPost && r.URL.Path == groups:
			writes.Add(1)
			var body map[string]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			input := body["security_group"]
			if len(input) != 2 || input["name"] != name || input["description"] != marker {
				t.Error("invalid project-scoped security group body")
			}
			if mode == "lost-response" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Openstack-Request-Id", "req-security-group")
			w.WriteHeader(http.StatusCreated)
			respond(w, fmt.Sprintf(`{"security_group":{"id":%q}}`, groupID), t)
		case r.Method == http.MethodGet && (r.URL.Path == groups || r.URL.Path == groups+"/"+groupID):
			owner, tenant, actualMarker, shared := project, project, marker, false
			rules := `,"security_group_rules":[{"direction":"egress"}]`
			switch mode {
			case "foreign":
				owner = "foreign-project"
			case "conflicting-project":
				tenant = "foreign-project"
			case "shared":
				shared = true
			case "other-marker":
				actualMarker = "unrelated"
			case "ingress":
				rules = `,"security_group_rules":[{"direction":"egress"},{"direction":"ingress"}]`
			case "missing-rules":
				rules = ""
			}
			group := fmt.Sprintf(`{"id":%q,"project_id":%q,"tenant_id":%q,"name":%q,"description":%q,"shared":%t%s}`,
				groupID, owner, tenant, name, actualMarker, shared, rules)
			if r.URL.Path == groups {
				q := r.URL.Query()
				if q.Get("project_id") != project || q.Get("description") != marker || q.Get("limit") != "2" {
					t.Error("unscoped Neutron group lookup")
				}
				if mode == "duplicate" {
					respond(w, `{"security_groups":[`+group+","+group+`]}`, t)
					return
				}
				respond(w, `{"security_groups":[`+group+`]}`, t)
				return
			}
			respond(w, `{"security_group":`+group+`}`, t)
		default:
			t.Errorf("unexpected Neutron request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: "RegionOne", Kind: provider.SecurityGroup}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-identity", allowNetworkCreation: true,
		endpoints: map[string]map[string]*url.URL{scope.Region: {"network": origin.JoinPath("network")}}}
	spec := provider.NetworkSpec{Name: name}
	ctx := t.Context()
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("network opt-in enabled group change: %v", err)
	}
	client.allowSecurityGroupCreation = true
	fixture.Store("lost-response")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); err == nil || writes.Load() != 1 {
		t.Fatalf("ambiguous change writes=%d error=%v", writes.Load(), err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); err != nil || obj.ID != groupID ||
		obj.Status != "available" || len(obj.Links) != 0 {
		t.Fatalf("invalid marker observation: %+v %v", obj, err)
	}
	fixture.Store("duplicate")
	if _, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("non-unique group accepted: %v", err)
	}
	fixture.Store("normal")
	receipt, err := client.CreateNetwork(ctx, scope, spec, token)
	if err != nil || receipt.ResourceID != groupID || receipt.RequestID != "req-security-group" || writes.Load() != 2 {
		t.Fatalf("invalid group receipt: %+v %v", receipt, err)
	}
	for _, mode := range []string{"foreign", "conflicting-project", "shared", "other-marker", "ingress", "missing-rules"} {
		fixture.Store(mode)
		if _, err := client.FindCreatedNetwork(ctx, scope, spec, token, groupID); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("%s evidence was accepted: %v", mode, err)
		}
	}
}
