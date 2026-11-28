package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func (f publicCloudResourceFixture) serveSecurityRules(t *testing.T, w http.ResponseWriter, r *http.Request, wrongScope bool, throttle func(bool) bool) bool {
	t.Helper()
	fail := func() bool {
		t.Errorf("incorrect security rule inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	rateLimited := func(resumed bool) bool {
		if !throttle(resumed) {
			return false
		}
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	var response any
	switch {
	case r.Host == "vpc.tencentcloudapi.com" && r.Header.Get("X-TC-Action") == "DescribeSecurityGroupPolicies":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Version") != "2017-03-12" {
			return fail()
		}
		var requested struct{ SecurityGroupId string }
		if json.Unmarshal(body, &requested) != nil || (requested.SecurityGroupId != "sg-1" && requested.SecurityGroupId != "sg-2") {
			return fail()
		}
		second := requested.SecurityGroupId == "sg-2"
		if rateLimited(second) {
			return true
		}
		result := map[string]any{"Version": "5"}
		if !second {
			result["Ingress"] = []any{map[string]any{
				"Protocol": "TCP", "Port": "443", "Action": "ACCEPT", "CidrBlock": "10.0.0.0/24", "PolicyIndex": 0,
			}}
		} else {
			result["Egress"] = []any{map[string]any{
				"Action": "DROP", "Ipv6CidrBlock": "2001:db8::/32", "Priority": 4,
				"ServiceTemplate": map[string]string{"ServiceId": "ppm-test"},
			}}
		}
		response = map[string]any{"Response": map[string]any{"SecurityGroupPolicySet": result}}
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeSecurityGroupAttribute":
		const ruleToken = "rule+/=page"
		q := r.URL.Query()
		group, token := q.Get("SecurityGroupId"), q.Get("NextToken")
		if (group != "sg-1" && group != "sg-2") || (token != "" && (token != ruleToken || group != "sg-1")) ||
			q.Get("Direction") != "all" || q.Get("MaxResults") != "100" || q.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return fail()
		}
		if group == "sg-1" && rateLimited(token != "") {
			return true
		}
		rows := []any{}
		next := ""
		switch {
		case group == "sg-2":
		case token == "":
			next = ruleToken
			rows = append(rows, map[string]string{
				"SecurityGroupRuleId": "sgr-1", "Direction": "ingress", "IpProtocol": "TCP",
				"Ipv6SourceCidrIp": "2001:db8::/32", "PortRange": "443/443", "Policy": "Accept",
			})
		default:
			rows = append(rows, map[string]string{
				"SecurityGroupRuleId": "sgr-2", "Direction": "egress", "IpProtocol": "UDP",
				"DestPrefixListId": "pl-fixture", "PortRangeListId": "prl-fixture", "Policy": "Drop", "Priority": "2",
			})
		}
		if wrongScope {
			group = "sg-foreign"
		}
		response = map[string]any{"RegionId": f.region, "SecurityGroupId": group, "NextToken": next,
			"Permissions": map[string]any{"Permission": rows}}
	case r.Host == "vpc.cn-north-4.myhuaweicloud.com" && r.URL.Path == "/v3/"+f.project+"/vpc/security-group-rules":
		const first = "60000000-0000-4000-8000-000000000001"
		q := r.URL.Query()
		marker := q.Get("marker")
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != f.token || q.Get("limit") != "100" ||
			(marker != "" && marker != first) {
			return fail()
		}
		if rateLimited(marker != "") {
			return true
		}
		project := f.project
		if wrongScope {
			project = "00000000000000000000000000000000"
		}
		row := map[string]any{"project_id": project, "security_group_id": f.id(provider.SecurityGroup), "priority": 1}
		page := map[string]any{"current_count": 1}
		if marker == "" {
			row["id"], row["direction"], row["protocol"], row["ethertype"] = first, "ingress", "tcp", "IPv4"
			row["remote_ip_prefix"], row["multiport"], row["action"], row["enabled"] = "10.0.0.0/24", "80,443", "allow", true
			page["next_marker"] = first
		} else {
			row["id"], row["direction"], row["ethertype"] = "60000000-0000-4000-8000-000000000002", "egress", "IPv6"
			row["remote_address_group_id"], row["action"], row["enabled"] = "70000000-0000-4000-8000-000000000001", "deny", false
		}
		response = map[string]any{"security_group_rules": []any{row}, "page_info": page}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
	return true
}

func (f publicCloudResourceFixture) checkRuleFields(t *testing.T, nodes []inventory.TopologyNode) {
	t.Helper()
	contains := func(parent, status string, fields ...string) {
		t.Helper()
		if !slices.ContainsFunc(nodes, func(node inventory.TopologyNode) bool {
			if node.Asset.ParentID != parent || node.Asset.Status != status {
				return false
			}
			for _, field := range fields {
				if !strings.Contains(node.Asset.Name, field) {
					return false
				}
			}
			return true
		}) {
			t.Errorf("no rule for %s with status %s and fields %v: %+v", parent, status, fields, nodes)
		}
	}
	group := f.id(provider.SecurityGroup)
	switch f.provider {
	case provider.TencentCVM:
		contains(group, "available", "ingress", "cidr=10.0.0.0/24", "ports=443", "action=accept")
		contains("sg-2", "available", "egress", "IPv6", "service=ppm-test", "action=drop", "priority=4")
	case provider.AlibabaECS:
		contains(group, "available", "ingress", "source_ipv6=2001:db8::/32", "ports=443/443", "action=accept")
		contains(group, "available", "egress", "dest_prefix_list=pl-fixture", "port_list=prl-fixture", "action=drop")
	case provider.HuaweiECS:
		contains(group, "available", "ingress", "cidr=10.0.0.0/24", "ports=80,443", "action=allow")
		contains(group, "disabled", "egress", "IPv6", "address_group=", "action=deny")
	}
}
