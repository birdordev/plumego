package openstack

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestNeutronSecurityRuleRevocation(t *testing.T) {
	const groupID, ruleID = "00000000-0000-0000-0000-000000000015", "00000000-0000-0000-0000-000000000016"
	const endpoint = "/network/v2.0/security-group-rules/" + ruleID
	var modes atomic.Value
	modes.Store("present")
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-project-token" {
			t.Error("Neutron call missing service identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mode := modes.Load().(string)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/network/v2.0/security-groups/"+groupID:
			project := "project"
			if mode == "foreign" {
				project = "foreign-project"
			}
			var rules string
			switch mode {
			case "missing", "disagree", "point-denied":
				rules = `,"security_group_rules":[]`
			case "omitted":
			default:
				rules = fmt.Sprintf(`,"security_group_rules":[{"id":%q,"direction":"ingress"}]`, ruleID)
			}
			respond(w, fmt.Sprintf(`{"security_group":{"id":%q,"project_id":%q,"name":"private","shared":%v%s}}`, groupID, project, mode == "shared", rules), t)
		case r.Method == http.MethodGet && r.URL.Path == endpoint:
			if mode == "missing" || mode == "omitted" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if mode == "point-denied" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			port := 443
			if mode == "changed" {
				port = 444
			}
			respond(w, fmt.Sprintf(`{"security_group_rule":{"id":%q,"security_group_id":%q,"project_id":"project",
			 "direction":"ingress","ethertype":"IPv4","protocol":"tcp","port_range_min":%d,"port_range_max":%d,
			 "remote_ip_prefix":"192.0.2.0/24"}}`, ruleID, groupID, port, port), t)
		case r.Method == http.MethodDelete && r.URL.Path == endpoint:
			writes.Add(1)
			w.Header().Set("X-Openstack-Request-Id", "req-rule-revoke")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected Neutron call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:project", Region: "RegionOne", Kind: provider.SecurityGroupRule}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-project-token", allowSecurityGroupRules: true,
		endpoints: map[string]map[string]*url.URL{scope.Region: {"network": base.JoinPath("network")}}}
	target := provider.SecurityRuleTarget{ID: ruleID, GroupID: groupID, Name: "ingress tcp 443-443 192.0.2.0/24"}
	ctx := t.Context()
	if _, err := client.PrepareSecurityRuleRevocation(ctx, scope, target); !provider.IsFailure(err, provider.DeletionDisabled) {
		t.Fatalf("creation opt-in enabled revoke: %v", err)
	}
	client.allowSecurityRuleRevocation = true
	for mode, want := range map[string]provider.FailureKind{"foreign": provider.Unauthorized, "shared": provider.DeletionBlocked, "changed": provider.DeletionBlocked} {
		modes.Store(mode)
		if send, err := client.PrepareSecurityRuleRevocation(ctx, scope, target); send != nil || !provider.IsFailure(err, want) {
			t.Fatalf("preflight %s unexpectedly passed: %v", mode, err)
		}
	}
	modes.Store("present")
	send, err := client.PrepareSecurityRuleRevocation(ctx, scope, target)
	if err != nil || send == nil {
		t.Fatal(err)
	}
	if id, err := send(ctx); err != nil || id != "req-rule-revoke" || writes.Load() != 1 {
		t.Fatalf("Neutron receipt=%q writes=%d error=%v", id, writes.Load(), err)
	}
	for _, mode := range []string{"present", "disagree"} {
		modes.Store(mode)
		if result, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); err != nil || result != provider.ActionPending {
			t.Fatalf("%s proof is not terminal: %s %v", mode, result, err)
		}
	}
	for mode, want := range map[string]provider.FailureKind{"omitted": provider.InvalidResponse, "point-denied": provider.Unauthorized} {
		modes.Store(mode)
		if result, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); result != provider.ActionPending || !provider.IsFailure(err, want) {
			t.Fatalf("%s response confirmed absence: %s %v", mode, result, err)
		}
	}
	modes.Store("missing")
	if result, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); err != nil || result != provider.ActionSucceeded {
		t.Fatalf("scoped absence not observed: %s %v", result, err)
	}
}
