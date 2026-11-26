package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/provider"
)

func (f *vpcCreationFixture) useGroup() {
	if f.vendor != provider.HuaweiECS {
		f.first, f.second = "sg-first", "sg-second"
	}
	if f.vendor == provider.AlibabaECS {
		f.parent = "vpc-parent"
	}
}

func (f *vpcCreationFixture) tencentGroup(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, action string, send func(any)) bool {
	t.Helper()
	marker := f.marker.Load()
	id, _ := f.coordinates()
	switch action {
	case "CreateSecurityGroup":
		var payload struct {
			GroupName, GroupDescription string
			Tags                        []struct{ Key, Value string }
		}
		if json.Unmarshal(body, &payload) != nil || payload.GroupName != fixtureVPCName || payload.GroupDescription == "" ||
			len(payload.Tags) != 1 || payload.Tags[0].Key != fixtureVPCTag || !f.remember(t, r, payload.Tags[0].Value, "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]any{"RequestId": fixtureVPCReceipt, "SecurityGroup": map[string]string{"SecurityGroupId": f.second}})
		}
	case "DescribeSecurityGroups":
		var payload struct {
			Limit            string
			SecurityGroupIds []string
			Filters          []struct {
				Name   string
				Values []string
			}
		}
		if marker == nil || json.Unmarshal(body, &payload) != nil || payload.Limit != "2" {
			return false
		}
		if f.writes.Load() == 1 {
			if len(payload.SecurityGroupIds) != 0 || len(payload.Filters) != 1 || payload.Filters[0].Name != "tag:"+fixtureVPCTag ||
				!slices.Equal(payload.Filters[0].Values, []string{*marker}) {
				return false
			}
		} else if !slices.Equal(payload.SecurityGroupIds, []string{id}) || len(payload.Filters) != 0 {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			row := map[string]any{"SecurityGroupId": id, "SecurityGroupName": fixtureVPCName, "IsDefault": false,
				"TagSet": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}}
			if f.writes.Load() > 1 {
				row["Region"] = f.region
			}
			rows = append(rows, row)
		}
		send(map[string]any{"SecurityGroupSet": rows, "TotalCount": len(rows)})
	case "DescribeSecurityGroupPolicies":
		var payload struct{ SecurityGroupId string }
		if !f.visible.Load() || json.Unmarshal(body, &payload) != nil || payload.SecurityGroupId != id {
			return false
		}
		ingress := []map[string]string{}
		if f.badEvidence.Load() {
			ingress = append(ingress, map[string]string{"Action": "ACCEPT", "CidrBlock": "0.0.0.0/0"})
		}
		send(map[string]any{"SecurityGroupPolicySet": map[string]any{"Version": "1", "Ingress": ingress, "Egress": []any{}}})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaGroup(t *testing.T, w http.ResponseWriter, r *http.Request, action string, send func(any)) bool {
	t.Helper()
	q := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2014-05-26" || q.Get("RegionId") != f.region {
		return false
	}
	marker := f.marker.Load()
	id, _ := f.coordinates()
	switch action {
	case "CreateSecurityGroup":
		if q.Get("VpcId") != f.parent || q.Get("SecurityGroupName") != fixtureVPCName || q.Get("SecurityGroupType") != "enterprise" ||
			q.Get("Tag.1.Key") != fixtureVPCTag || !f.remember(t, r, q.Get("Tag.1.Value"), q.Get("ClientToken")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]string{"RequestId": fixtureVPCReceipt, "SecurityGroupId": f.second})
		}
	case "DescribeSecurityGroups":
		if marker == nil || q.Get("VpcId") != f.parent || q.Get("MaxResults") != "10" {
			return false
		}
		if f.writes.Load() == 1 {
			if q.Has("SecurityGroupId") || q.Get("Tag.1.Key") != fixtureVPCTag || q.Get("Tag.1.Value") != *marker {
				return false
			}
		} else if q.Get("SecurityGroupId") != id || q.Has("Tag.1.Key") {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			rows = append(rows, map[string]any{"SecurityGroupId": id, "SecurityGroupName": fixtureVPCName,
				"VpcId": f.parent, "SecurityGroupType": "enterprise",
				"Tags": map[string]any{"Tag": []map[string]string{{"TagKey": fixtureVPCTag, "TagValue": *marker}}}})
		}
		send(map[string]any{"RegionId": f.region, "SecurityGroups": map[string]any{"SecurityGroup": rows}})
	case "DescribeSecurityGroupAttribute":
		if !f.visible.Load() || q.Get("SecurityGroupId") != id || q.Get("Direction") != "ingress" || q.Get("MaxResults") != "10" {
			return false
		}
		policy := "Drop"
		if f.badEvidence.Load() {
			policy = "Accept"
		}
		send(map[string]any{"RegionId": f.region, "VpcId": f.parent, "SecurityGroupId": id, "InnerAccessPolicy": policy,
			"Permissions": map[string]any{"Permission": []any{}}})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiGroup(t *testing.T, w http.ResponseWriter, r *http.Request, send func(any)) bool {
	t.Helper()
	path := "/v3/" + fixtureHuaweiProject + "/vpc/security-groups"
	if r.URL.Path == path && r.Method == http.MethodPost {
		var payload struct {
			Group struct{ Name, Description string } `json:"security_group"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&payload) != nil || r.Header.Get("Content-Type") != "application/json" ||
			payload.Group.Name != fixtureVPCName || !strings.HasPrefix(payload.Group.Description, fixtureVPCTag+":") ||
			!f.remember(t, r, strings.TrimPrefix(payload.Group.Description, fixtureVPCTag+":"), "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"request_id": fixtureVPCReceipt, "security_group": map[string]string{"id": f.second, "project_id": fixtureHuaweiProject}})
		}
		return true
	}
	marker := f.marker.Load()
	if marker == nil || r.Method != http.MethodGet {
		return false
	}
	id, _ := f.coordinates()
	description := fixtureVPCTag + ":" + *marker
	row := map[string]any{"id": id, "name": fixtureVPCName, "project_id": fixtureHuaweiProject, "description": description}
	switch r.URL.Path {
	case path:
		q := r.URL.Query()
		if f.writes.Load() != 1 || q.Get("description") != description || q.Get("limit") != "2" {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			rows = append(rows, row)
		}
		send(map[string]any{"security_groups": rows, "page_info": map[string]int{"current_count": len(rows)}})
	case path + "/" + id:
		if !f.visible.Load() || r.URL.RawQuery != "" {
			return false
		}
		ingress := map[string]string{"id": "20000000-0000-4000-8000-000000000001", "project_id": fixtureHuaweiProject,
			"security_group_id": id, "direction": "ingress", "action": "allow", "remote_group_id": id}
		if f.badEvidence.Load() {
			delete(ingress, "remote_group_id")
			ingress["remote_ip_prefix"] = "0.0.0.0/0"
		}
		row["security_group_rules"] = []map[string]string{ingress, {"id": "20000000-0000-4000-8000-000000000002",
			"project_id": fixtureHuaweiProject, "security_group_id": id, "direction": "egress", "action": "allow"}}
		send(map[string]any{"security_group": row})
	default:
		return false
	}
	return true
}
