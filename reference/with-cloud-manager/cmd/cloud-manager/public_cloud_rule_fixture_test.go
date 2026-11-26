package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/provider"
)

const fixtureFirstRuleCIDR = "192.0.2.0/24"
const fixtureSecondRuleCIDR = "198.51.100.0/24"
const fixtureRuleMarker = "plumego-cmp-rule:"

func fixtureSecurityRule(cidr string) provider.SecurityRule {
	rule := provider.SecurityRule{Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 443, RemoteCIDR: cidr}
	if cidr == fixtureSecondRuleCIDR {
		rule.Direction, rule.Protocol, rule.FromPort, rule.ToPort = "egress", "udp", 1000, 1002
	}
	return rule
}

func (f *vpcCreationFixture) useRule() {
	f.parent = "sg-parent"
	if f.vendor == provider.HuaweiECS {
		f.parent = "20000000-0000-4000-8000-000000000020"
	} else {
		f.first, f.second = "sgr-first", "sgr-second"
	}
}

func (f *vpcCreationFixture) matchesCreatedID(actual, expected string) bool {
	if f.kind == provider.SecurityGroupRule && f.vendor == provider.TencentCVM {
		return strings.HasPrefix(actual, "tc-sgr-") && strings.HasSuffix(actual, "-1") && len(actual) == 73
	}
	return actual == expected
}

func (f *vpcCreationFixture) rememberRule(t *testing.T, r *http.Request, marker, token string) bool {
	t.Helper()
	digest, found := strings.CutPrefix(marker, fixtureRuleMarker)
	return found && f.remember(t, r, digest, token)
}

func (f *vpcCreationFixture) tencentRule(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, action string, send func(any)) bool {
	t.Helper()
	switch action {
	case "DescribeSecurityGroups":
		var query struct{ SecurityGroupIds []string }
		if json.Unmarshal(body, &query) != nil || !slices.Equal(query.SecurityGroupIds, []string{f.parent}) || !f.replayMode(t, r) {
			return false
		}
		row := map[string]any{"SecurityGroupId": f.parent, "Region": f.region, "IsDefault": f.blockedGroup.Load()}
		send(map[string]any{"TotalCount": 1, "SecurityGroupSet": []any{row}})
	case "DescribeSecurityGroupPolicies":
		var query struct{ SecurityGroupId string }
		if json.Unmarshal(body, &query) != nil || query.SecurityGroupId != f.parent {
			return false
		}
		if f.invalidAbsence.Load() {
			send(map[string]any{"SecurityGroupPolicySet": map[string]string{"Version": "8"}})
			return true
		}
		ingress, egress := []any{}, []any{}
		if marker := f.marker.Load(); marker != nil && f.visible.Load() {
			row := map[string]any{"PolicyDescription": fixtureRuleMarker + *marker, "Action": "ACCEPT", "PolicyIndex": 0}
			if f.writes.Load() == 1 {
				row["Protocol"], row["Port"], row["CidrBlock"] = "TCP", "443", fixtureFirstRuleCIDR
				ingress = append(ingress, row)
			} else {
				row["Protocol"], row["Port"], row["CidrBlock"] = "UDP", "1000-1002", fixtureSecondRuleCIDR
				if f.badEvidence.Load() {
					row["CidrBlock"] = "0.0.0.0/0"
				}
				egress = append(egress, row)
				if f.duplicateRule.Load() {
					duplicate := map[string]any{"PolicyDescription": row["PolicyDescription"], "Action": row["Action"],
						"Protocol": row["Protocol"], "Port": row["Port"], "CidrBlock": row["CidrBlock"], "PolicyIndex": 1}
					egress = append(egress, duplicate)
				}
			}
		}
		if f.revokes.Load() > 0 && !f.visible.Load() {
			ingress = append(ingress, map[string]any{"PolicyDescription": "retained", "Protocol": "TCP", "Port": "443",
				"CidrBlock": fixtureFirstRuleCIDR, "PolicyIndex": 0, "Action": "ACCEPT"})
		}
		send(map[string]any{"SecurityGroupPolicySet": map[string]any{"Version": "7", "Ingress": ingress, "Egress": egress}})
	case "DeleteSecurityGroupPolicies":
		var data struct {
			SecurityGroupId        string
			SecurityGroupPolicySet struct {
				Version string
				Ingress []struct{ PolicyIndex int }
				Egress  []struct{ PolicyIndex *int }
			}
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || data.SecurityGroupId != f.parent || data.SecurityGroupPolicySet.Version != "7" ||
			len(data.SecurityGroupPolicySet.Ingress) != 0 || len(data.SecurityGroupPolicySet.Egress) != 1 ||
			data.SecurityGroupPolicySet.Egress[0].PolicyIndex == nil || *data.SecurityGroupPolicySet.Egress[0].PolicyIndex != 0 ||
			f.writes.Load() != 2 || !f.revokes.CompareAndSwap(0, 1) {
			return false
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	case "CreateSecurityGroupPolicies":
		type entry struct{ Protocol, Port, CidrBlock, Action, PolicyDescription string }
		var payload struct {
			SecurityGroupId        string
			SecurityGroupPolicySet struct {
				Version         string
				Ingress, Egress []entry
			}
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&payload) != nil || payload.SecurityGroupId != f.parent || payload.SecurityGroupPolicySet.Version != "7" {
			return false
		}
		in, out := payload.SecurityGroupPolicySet.Ingress, payload.SecurityGroupPolicySet.Egress
		switch f.writes.Load() {
		case 0:
			if len(in) != 1 || len(out) != 0 || in[0].Protocol != "TCP" || in[0].Port != "443" || in[0].Action != "ACCEPT" ||
				in[0].CidrBlock != fixtureFirstRuleCIDR || !f.rememberRule(t, r, in[0].PolicyDescription, "") {
				return false
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case 1:
			if len(in) != 0 || len(out) != 1 || out[0].Protocol != "UDP" || out[0].Port != "1000-1002" || out[0].Action != "ACCEPT" ||
				out[0].CidrBlock != fixtureSecondRuleCIDR || !f.rememberRule(t, r, out[0].PolicyDescription, "") {
				return false
			}
			send(map[string]string{"RequestId": fixtureVPCReceipt})
		default:
			return false
		}
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaRule(t *testing.T, w http.ResponseWriter, r *http.Request, action string, send func(any)) bool {
	t.Helper()
	q := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2014-05-26" ||
		q.Get("RegionId") != f.region || q.Get("SecurityGroupId") != f.parent {
		return false
	}
	switch action {
	case "DescribeSecurityGroups":
		if q.Get("MaxResults") != "10" || !f.replayMode(t, r) {
			return false
		}
		groupType := "enterprise"
		if f.blockedGroup.Load() {
			groupType = "normal"
		}
		groups := []map[string]string{{"SecurityGroupId": f.parent, "VpcId": "vpc-parent", "SecurityGroupType": groupType}}
		send(map[string]any{"RegionId": f.region, "SecurityGroups": map[string]any{"SecurityGroup": groups}})
	case "AuthorizeSecurityGroup", "AuthorizeSecurityGroupEgress":
		expectedAction, cidr, port, protocol := "AuthorizeSecurityGroup", fixtureFirstRuleCIDR, "443/443", "TCP"
		remote, opposite := "SourceCidrIp", "DestCidrIp"
		if f.writes.Load() == 1 {
			expectedAction, cidr, port, protocol = "AuthorizeSecurityGroupEgress", fixtureSecondRuleCIDR, "1000/1002", "UDP"
			remote, opposite = opposite, remote
		}
		if action != expectedAction || q.Get("Permissions.1."+remote) != cidr || q.Has("Permissions.1."+opposite) ||
			q.Get("Permissions.1.PortRange") != port || q.Get("Permissions.1.IpProtocol") != protocol ||
			q.Get("Permissions.1.Policy") != "accept" || q.Get("Permissions.1.Priority") != "100" ||
			!f.rememberRule(t, r, q.Get("Permissions.1.Description"), q.Get("ClientToken")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]string{"RequestId": fixtureVPCReceipt})
		}
	case "RevokeSecurityGroupEgress":
		if q.Get("SecurityGroupRuleId.1") != f.second || len(q) != 3 ||
			f.writes.Load() != 2 || !f.revokes.CompareAndSwap(0, 1) {
			return false
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	case "DescribeSecurityGroupAttribute":
		marker := f.marker.Load()
		if marker == nil || q.Get("Direction") != "all" || q.Get("MaxResults") != "100" {
			return false
		}
		rows := []map[string]string{}
		next := ""
		if f.visible.Load() && f.writes.Load() == 1 && q.Get("NextToken") == "" {
			next = "other-page"
			rows = append(rows, map[string]string{"SecurityGroupRuleId": "sgr-unrelated", "Direction": "ingress"})
		} else if f.visible.Load() {
			row := map[string]string{"Description": fixtureRuleMarker + *marker, "NicType": "intranet", "Priority": "100", "Policy": "Accept"}
			if f.writes.Load() == 1 {
				if q.Get("NextToken") != "other-page" {
					return false
				}
				row["SecurityGroupRuleId"], row["Direction"], row["IpProtocol"] = f.first, "ingress", "TCP"
				row["SourceCidrIp"], row["PortRange"] = fixtureFirstRuleCIDR, "443/443"
			} else {
				row["SecurityGroupRuleId"], row["Direction"], row["IpProtocol"] = f.second, "egress", "UDP"
				row["DestCidrIp"], row["PortRange"] = fixtureSecondRuleCIDR, "1000/1002"
				if f.badEvidence.Load() {
					row["DestCidrIp"] = "0.0.0.0/0"
				}
			}
			rows = append(rows, row)
		}
		if f.revokes.Load() > 0 && !f.visible.Load() {
			switch q.Get("NextToken") {
			case "":
				rows = append(rows, map[string]string{"SecurityGroupRuleId": "sgr-retained", "Direction": "ingress"})
				next = "retained-page"
			case "retained-page":
				rows = append(rows, map[string]string{"SecurityGroupRuleId": "sgr-retained-other", "Direction": "egress"})
			default:
				return false
			}
		}
		vpc := "vpc-parent"
		if f.invalidAbsence.Load() {
			vpc = "vpc-foreign"
		}
		send(map[string]any{"RegionId": f.region, "SecurityGroupId": f.parent, "VpcId": vpc, "NextToken": next,
			"Permissions": map[string]any{"Permission": rows}})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiRule(t *testing.T, w http.ResponseWriter, r *http.Request, send func(any)) bool {
	t.Helper()
	base := "/v3/" + fixtureHuaweiProject + "/vpc/"
	rulePath, groupPath := base+"security-group-rules", base+"security-groups/"+f.parent
	if r.Method == http.MethodDelete && r.URL.Path == rulePath+"/"+f.second {
		if r.URL.RawQuery != "" || f.writes.Load() != 2 || !f.revokes.CompareAndSwap(0, 1) {
			return false
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == rulePath {
		var data struct {
			Rule map[string]any `json:"security_group_rule"`
		}
		if json.NewDecoder(r.Body).Decode(&data) != nil || r.Header.Get("Content-Type") != "application/json" {
			return false
		}
		row := data.Rule
		direction, protocol, cidr, port := "ingress", "tcp", fixtureFirstRuleCIDR, "443"
		if f.writes.Load() == 1 {
			direction, protocol, cidr, port = "egress", "udp", fixtureSecondRuleCIDR, "1000-1002"
		}
		marker, _ := row["description"].(string)
		if len(row) != 10 || row["security_group_id"] != f.parent || row["direction"] != direction ||
			row["ethertype"] != "IPv4" || row["protocol"] != protocol || row["remote_ip_prefix"] != cidr ||
			row["multiport"] != port || row["action"] != "allow" || row["enabled"] != true || row["priority"] != float64(100) ||
			!f.rememberRule(t, r, marker, "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"request_id": fixtureVPCReceipt, "security_group_rule": map[string]string{
				"id": f.second, "project_id": fixtureHuaweiProject, "security_group_id": f.parent}})
		}
		return true
	}
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		return false
	}
	rows := []map[string]any{}
	if marker := f.marker.Load(); marker != nil && (f.visible.Load() || (r.URL.Path != groupPath && f.holdRulePoint.Load())) {
		row := map[string]any{"project_id": fixtureHuaweiProject, "security_group_id": f.parent, "description": fixtureRuleMarker + *marker,
			"ethertype": "IPv4", "action": "allow", "enabled": true, "priority": 100}
		if f.writes.Load() == 1 {
			row["id"], row["direction"], row["protocol"], row["remote_ip_prefix"], row["multiport"] = f.first, "ingress", "tcp", fixtureFirstRuleCIDR, "443"
		} else {
			row["id"], row["direction"], row["protocol"], row["remote_ip_prefix"], row["multiport"] = f.second, "egress", "udp", fixtureSecondRuleCIDR, "1000-1002"
			if f.badEvidence.Load() {
				row["remote_ip_prefix"] = "0.0.0.0/0"
			}
		}
		rows = append(rows, row)
	}
	switch r.URL.Path {
	case groupPath:
		if !f.replayMode(t, r) {
			return false
		}
		name := "rule-parent"
		if f.blockedGroup.Load() {
			name = "default"
		}
		project := fixtureHuaweiProject
		if f.invalidAbsence.Load() {
			project = "00000000000000000000000000000000"
		}
		send(map[string]any{"security_group": map[string]any{
			"id": f.parent, "name": name, "project_id": project, "security_group_rules": rows}})
	case rulePath + "/" + f.second:
		if f.revokes.Load() > 0 && len(rows) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		if f.writes.Load() != 2 || len(rows) != 1 {
			return false
		}
		send(map[string]any{"security_group_rule": rows[0]})
	default:
		return false
	}
	return true
}
