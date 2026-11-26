package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/provider"
)

const (
	fixtureParentPrimary    = "10.0.0.0/16"
	fixtureParentExtra      = "10.1.0.0/16"
	fixtureSubnetCIDR       = "10.1.1.0/24"
	fixtureSecondSubnetCIDR = "10.1.2.0/24"
)

func (f *vpcCreationFixture) useSubnet() {
	switch f.vendor {
	case provider.TencentCVM:
		f.parent, f.first, f.second, f.zone = "vpc-parent", "subnet-first", "subnet-second", "ap-guangzhou-3"
	case provider.AlibabaECS:
		f.parent, f.first, f.second, f.zone = "vpc-parent", "vsw-first", "vsw-second", "cn-hangzhou-b"
	case provider.HuaweiECS:
		f.parent, f.zone = "20000000-0000-4000-8000-000000000009", "cn-north-4a"
	}
}

func (f *vpcCreationFixture) pendingSubnetCIDR() string {
	if f.writes.Load() == 0 {
		return fixtureSubnetCIDR
	}
	return fixtureSecondSubnetCIDR
}

func (f *vpcCreationFixture) subnetCoordinates() (string, string) {
	if f.writes.Load() <= 1 {
		return f.first, fixtureSubnetCIDR
	}
	if f.badEvidence.Load() {
		return f.second, "10.1.3.0/24"
	}
	return f.second, fixtureSecondSubnetCIDR
}

func (f *vpcCreationFixture) tencentSubnet(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, action string, send func(any)) bool {
	t.Helper()
	const numericZone = "100003"
	if action == "DescribeZones" && r.Host == "cvm.tencentcloudapi.com" {
		send(map[string]any{"TotalCount": 1, "ZoneSet": []map[string]string{{"Zone": f.zone, "ZoneId": numericZone, "ZoneState": "AVAILABLE"}}})
		return true
	}
	if r.Host != "vpc.tencentcloudapi.com" || r.Header.Get("X-TC-Version") != "2017-03-12" {
		return false
	}
	switch action {
	case "DescribeVpcs":
		var query struct{ VpcIds []string }
		if json.Unmarshal(body, &query) != nil || !slices.Equal(query.VpcIds, []string{f.parent}) || !f.replayMode(t, r) {
			return false
		}
		send(map[string]any{"TotalCount": 1, "VpcSet": []map[string]any{{"VpcId": f.parent, "CidrBlock": fixtureParentPrimary,
			"AssistantCidrSet": []map[string]string{{"CidrBlock": fixtureParentExtra}}}}})
	case "CreateSubnet":
		var input struct {
			SubnetName, VpcId, Zone, CidrBlock string
			Tags                               []struct{ Key, Value string }
		}
		if json.Unmarshal(body, &input) != nil || input.SubnetName != fixtureVPCName || input.VpcId != f.parent ||
			input.Zone != f.zone || input.CidrBlock != f.pendingSubnetCIDR() || len(input.Tags) != 1 ||
			input.Tags[0].Key != fixtureVPCTag || !f.remember(t, r, input.Tags[0].Value, "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]any{"RequestId": fixtureVPCReceipt, "Subnet": map[string]string{"SubnetId": f.second}})
		}
	case "DescribeSubnets":
		var query struct {
			Limit     string
			SubnetIds []string
			Filters   []struct {
				Name   string
				Values []string
			}
		}
		marker := f.marker.Load()
		if marker == nil || json.Unmarshal(body, &query) != nil || query.Limit != "2" {
			return false
		}
		if f.writes.Load() == 1 {
			if len(query.SubnetIds) != 0 || len(query.Filters) != 2 || query.Filters[0].Name != "tag:"+fixtureVPCTag ||
				!slices.Equal(query.Filters[0].Values, []string{*marker}) || query.Filters[1].Name != "vpc-id" ||
				!slices.Equal(query.Filters[1].Values, []string{f.parent}) {
				return false
			}
		} else if len(query.Filters) != 0 || !slices.Equal(query.SubnetIds, []string{f.second}) {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			id, cidr := f.subnetCoordinates()
			rows = append(rows, map[string]any{"SubnetId": id, "SubnetName": fixtureVPCName, "VpcId": f.parent, "Zone": numericZone,
				"CidrBlock": cidr, "TagSet": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}})
		}
		send(map[string]any{"TotalCount": len(rows), "SubnetSet": rows})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaSubnet(t *testing.T, r *http.Request, action string, w http.ResponseWriter, send func(any)) bool {
	t.Helper()
	q := r.URL.Query()
	if q.Get("VpcId") != f.parent {
		return false
	}
	switch action {
	case "DescribeVpcs":
		if q.Get("VpcOwnerId") != f.account || q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" || !f.replayMode(t, r) {
			return false
		}
		rows := []map[string]any{{"VpcId": f.parent, "OwnerId": json.Number(f.account), "Status": "Available", "RegionId": f.region,
			"CidrBlock": fixtureParentPrimary, "SecondaryCidrBlocks": map[string][]string{"SecondaryCidrBlock": {fixtureParentExtra}}}}
		send(map[string]any{"Vpcs": map[string]any{"Vpc": rows}, "TotalCount": 1, "PageNumber": 1, "PageSize": 10})
	case "CreateVSwitch":
		if q.Get("VSwitchName") != fixtureVPCName || q.Get("CidrBlock") != f.pendingSubnetCIDR() || q.Get("ZoneId") != f.zone ||
			q.Get("Tag.1.Key") != fixtureVPCTag || !f.remember(t, r, q.Get("Tag.1.Value"), q.Get("ClientToken")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]string{"RequestId": fixtureVPCReceipt, "VSwitchId": f.second})
		}
	case "DescribeVSwitches":
		marker := f.marker.Load()
		if marker == nil || q.Get("VSwitchOwnerId") != f.account || q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" {
			return false
		}
		if f.writes.Load() == 1 {
			if q.Has("VSwitchId") || q.Get("Tag.1.Key") != fixtureVPCTag || q.Get("Tag.1.Value") != *marker {
				return false
			}
		} else if q.Get("VSwitchId") != f.second || q.Has("Tag.1.Key") {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			id, cidr := f.subnetCoordinates()
			rows = append(rows, map[string]any{"VSwitchId": id, "VSwitchName": fixtureVPCName, "VpcId": f.parent, "CidrBlock": cidr,
				"ZoneId": f.zone, "OwnerId": json.Number(f.account), "Status": "Available",
				"Tags": map[string]any{"Tag": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}}})
		}
		send(map[string]any{"VSwitches": map[string]any{"VSwitch": rows}, "TotalCount": len(rows), "PageNumber": 1, "PageSize": 10})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiSubnet(t *testing.T, r *http.Request, w http.ResponseWriter, send func(any)) bool {
	t.Helper()
	v3 := "/v3/" + fixtureHuaweiProject + "/vpc/"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == v3+"vpcs":
		if r.URL.Query().Get("id") != f.parent || r.URL.Query().Get("limit") != "2" || !f.replayMode(t, r) {
			return false
		}
		rows := []map[string]any{{"id": f.parent, "cidr": fixtureParentPrimary, "extend_cidrs": []string{fixtureParentExtra},
			"project_id": fixtureHuaweiProject, "status": "ACTIVE"}}
		send(map[string]any{"vpcs": rows, "page_info": map[string]int{"current_count": 1}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/subnets":
		var payload struct{ Subnet map[string]any }
		if json.NewDecoder(r.Body).Decode(&payload) != nil || r.Header.Get("Content-Type") != "application/json" {
			return false
		}
		s := payload.Subnet
		description, ok := s["description"].(string)
		gateway := "10.1.1.1"
		if f.writes.Load() > 0 {
			gateway = "10.1.2.1"
		}
		if !ok || s["name"] != fixtureVPCName || s["cidr"] != f.pendingSubnetCIDR() || s["gateway_ip"] != gateway ||
			s["availability_zone"] != f.zone || s["vpc_id"] != f.parent || s["ipv6_enable"] != false || s["dhcp_enable"] != true ||
			!strings.HasPrefix(description, fixtureVPCTag+":") || !f.remember(t, r, strings.TrimPrefix(description, fixtureVPCTag+":"), "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"request_id": fixtureVPCReceipt, "subnet": map[string]string{"id": f.second, "tenant_id": fixtureHuaweiProject}})
		}
	case r.Method == http.MethodGet && r.URL.Path == v3+"virsubnets":
		q, marker := r.URL.Query(), f.marker.Load()
		if marker == nil || q.Get("limit") != "2" || q.Get("vpc_id") != f.parent {
			return false
		}
		description := fixtureVPCTag + ":" + *marker
		if f.writes.Load() == 1 {
			if q.Has("id") || q.Get("description") != description {
				return false
			}
		} else if q.Get("id") != f.second || q.Has("description") {
			return false
		}
		rows := []map[string]any{}
		if f.visible.Load() {
			id, cidr := f.subnetCoordinates()
			gateway := "10.1.1.1"
			if f.writes.Load() > 1 {
				gateway = "10.1.2.1"
			}
			rows = append(rows, map[string]any{"id": id, "name": fixtureVPCName, "description": description,
				"project_id": fixtureHuaweiProject, "vpc_id": f.parent, "zone_id": f.zone, "status": "ACTIVE",
				"subnet_cidrs": []map[string]any{{"id": fixtureHuaweiIPv4, "cidr": cidr, "gateway_ip": gateway, "ip_version": "4", "enable_dhcp": true}}})
		}
		send(map[string]any{"virsubnets": rows, "page_info": map[string]int{"current_count": len(rows)}})
	default:
		return false
	}
	return true
}
