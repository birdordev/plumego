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

func TestNeutronSecurityRuleDiscovery(t *testing.T) {
	const project = "tenant-project"
	const group = "00000000-0000-0000-0000-000000000031"
	const tcp, ipv6 = "00000000-0000-0000-0000-000000000041", "00000000-0000-0000-0000-000000000042"
	const rules = "/network/v2.0/security-group-rules"
	var mode atomic.Value
	mode.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != "fixture-scope" {
			t.Error("unexpected Neutron inventory request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		scenario := mode.Load().(string)
		owner := project
		if scenario == "other-project" {
			owner = "outside"
		}
		base := fmt.Sprintf(`"project_id":%q,"security_group_id":%q`, owner, group)
		first := fmt.Sprintf(`{%s,"id":%q,"direction":"ingress","ethertype":"IPv4","protocol":"tcp","port_range_min":443,
			"port_range_max":443,"remote_ip_prefix":"192.0.2.0/24","description":"fixture-private-marker"}`, base, tcp)
		last := fmt.Sprintf(`{%s,"id":%q,"direction":"egress","ethertype":"IPv6","protocol":null,
			"port_range_min":null,"port_range_max":null,"remote_ip_prefix":null}`, base, ipv6)
		switch r.URL.Path {
		case rules:
			q := r.URL.Query()
			if q.Get("project_id") != project || q.Get("limit") != "200" {
				t.Error("initial or resumed Neutron query lost its project filter")
			}
			if q.Get("marker") == tcp {
				respond(w, `{"security_group_rules":[`+last+`]}`, t)
				return
			}
			var next string
			switch scenario {
			case "outside-url":
				next = "https://ignored.example" + rules + "?marker=" + tcp
			case "wrong-marker":
				next = rules + "?marker=" + ipv6
			default:
				// 恢复用本地作用域，忽略厂商链接中附带的旧项目参数。
				next = rules + "?project_id=outdated&marker=" + tcp
			}
			respond(w, fmt.Sprintf(`{"security_group_rules":[%s],"security_group_rules_links":[{"rel":"next","href":%q}]}`, first, next), t)
		case rules + "/" + ipv6:
			if scenario == "missing" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			respond(w, `{"security_group_rule":`+last+`}`, t)
		default:
			t.Errorf("unexpected Neutron endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: "RegionOne", Kind: provider.SecurityGroupRule}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-scope",
		endpoints: map[string]map[string]*url.URL{scope.Region: {"network": base.JoinPath("network")}}}
	first, err := client.Discover(t.Context(), scope, "")
	if err != nil || first.Next != tcp || len(first.Objects) != 1 || first.Objects[0].ID != tcp ||
		first.Objects[0].Name != "ingress tcp 443-443 192.0.2.0/24" || first.Objects[0].Tags != nil {
		t.Fatalf("first page %+v: %v", first, err)
	}
	if parent, ok := provider.SecurityRuleParent(first.Objects[0]); !ok || parent != group {
		t.Fatalf("unexpected parent group in Neutron response: %v", first.Objects)
	}
	last, err := client.Discover(t.Context(), scope, first.Next)
	if err != nil || last.Next != "" || len(last.Objects) != 1 || last.Objects[0].ID != ipv6 || last.Objects[0].Name != "egress any IPv6 any" {
		t.Fatalf("second page %+v: %v", last, err)
	}
	if observed, err := client.Observe(t.Context(), scope, ipv6); err != nil || observed.Name != last.Objects[0].Name {
		t.Fatalf("point observation returned %+v: %v", observed, err)
	}
	mode.Store("other-project")
	if got, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.Unauthorized) || len(got.Objects) != 0 {
		t.Fatalf("foreign project rules published %+v: %v", got, err)
	}
	for _, scenario := range []string{"outside-url", "wrong-marker"} {
		mode.Store(scenario)
		if _, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("unsafe Neutron continuation (%s) returned %v", scenario, err)
		}
	}
	mode.Store("missing")
	if _, err := client.Observe(t.Context(), scope, ipv6); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("Neutron 404 was treated as authoritative: %v", err)
	}
}

func TestNeutronSecurityGroupRuleCreation(t *testing.T) {
	const token, project, region = "FixtureRuleIntent1234", "test-project", "RegionOne"
	const group, ruleID = "00000000-0000-0000-0000-000000000033", "00000000-0000-0000-0000-000000000037"
	const marker, cidr = neutronIntentPrefix + token, "192.0.2.0/24"
	const rules = "/network/v2.0/security-group-rules"
	var writes atomic.Int32
	var fixture atomic.Value
	fixture.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-identity" {
			t.Error("Neutron request without scoped token")
		}
		mode := fixture.Load().(string)
		owner, tenant, actualMarker := project, project, marker
		if mode == "foreign" {
			owner = "foreign-project"
		}
		if mode == "conflicting-project" {
			tenant = "foreign-project"
		}
		if mode == "other-marker" {
			actualMarker = "unrelated"
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/network/v2.0/extensions/"+neutronDescriptionExtension:
			respond(w, fmt.Sprintf(`{"extension":{"alias":%q}}`, neutronDescriptionExtension), t)
		case r.Method == http.MethodGet && r.URL.Path == "/network/v2.0/security-groups/"+group:
			respond(w, fmt.Sprintf(`{"security_group":{"id":%q,"project_id":%q,"tenant_id":%q,"name":"private","shared":%t}}`,
				group, owner, tenant, mode == "shared"), t)
		case r.Method == http.MethodPost && r.URL.Path == rules:
			writes.Add(1)
			var body map[string]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			input := body["security_group_rule"]
			for key, want := range map[string]any{"security_group_id": group, "description": marker, "direction": "egress",
				"ethertype": "IPv4", "protocol": "udp", "port_range_min": float64(53), "port_range_max": float64(53), "remote_ip_prefix": cidr} {
				if input[key] != want {
					t.Errorf("incorrect rule field %s: got %v want %v", key, input[key], want)
				}
			}
			if len(input) != 8 {
				t.Error("sent fields outside the scoped rule contract")
			}
			if mode == "lost-response" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Openstack-Request-Id", "req-rule")
			w.WriteHeader(http.StatusCreated)
			respond(w, fmt.Sprintf(`{"security_group_rule":{"id":%q}}`, ruleID), t)
		case r.Method == http.MethodGet && (r.URL.Path == rules || r.URL.Path == rules+"/"+ruleID):
			actualCIDR := cidr
			if mode == "wrong-cidr" {
				actualCIDR = "192.0.2.1/32"
			}
			row := fmt.Sprintf(`{"id":%q,"project_id":%q,"tenant_id":%q,"security_group_id":%q,"description":%q,
				"direction":"egress","ethertype":"IPv4","protocol":"udp","port_range_min":53,"port_range_max":53,"remote_ip_prefix":%q}`,
				ruleID, owner, tenant, group, actualMarker, actualCIDR)
			if r.URL.Path != rules {
				respond(w, `{"security_group_rule":`+row+`}`, t)
				return
			}
			q := r.URL.Query()
			if q.Get("project_id") != project || q.Get("security_group_id") != group || q.Get("description") != marker || q.Get("limit") != "2" {
				t.Error("unscoped Neutron rule lookup")
			}
			if mode == "duplicate" {
				row += "," + row
			}
			respond(w, `{"security_group_rules":[`+row+`]}`, t)
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
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: region, Kind: provider.SecurityGroupRule}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-identity", allowSecurityGroupCreation: true,
		endpoints: map[string]map[string]*url.URL{region: {"network": origin.JoinPath("network")}}}
	spec := provider.NetworkSpec{ParentID: group, Rule: &provider.SecurityRule{Direction: "egress", Protocol: "udp", FromPort: 53, ToPort: 53, RemoteCIDR: cidr}}
	ctx := t.Context()
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("group creation enabled rule changes: %v", err)
	}
	client.allowSecurityGroupRules = true
	for _, mode := range []string{"foreign", "conflicting-project", "shared"} {
		fixture.Store(mode)
		if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
			t.Fatalf("%s parent accepted: %v", mode, err)
		}
	}
	fixture.Store("lost-response")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); err == nil || writes.Load() != 1 {
		t.Fatalf("send repeated after lost response: writes=%d err=%v", writes.Load(), err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); err != nil || obj.ID != ruleID ||
		len(obj.Links) != 1 || obj.Links[0].ID != group || obj.Links[0].Kind != provider.SecurityGroup {
		t.Fatalf("invalid recovered rule: %+v %v", obj, err)
	}
	for _, mode := range []string{"conflicting-project", "other-marker", "wrong-cidr", "duplicate"} {
		fixture.Store(mode)
		if _, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("%s evidence accepted: %v", mode, err)
		}
	}
	fixture.Store("normal")
	if receipt, err := client.CreateNetwork(ctx, scope, spec, token); err != nil || receipt.ResourceID != ruleID || receipt.RequestID != "req-rule" {
		t.Fatalf("invalid Neutron rule receipt: %+v %v", receipt, err)
	}
}
