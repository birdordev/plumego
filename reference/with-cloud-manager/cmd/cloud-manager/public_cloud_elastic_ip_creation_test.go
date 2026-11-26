package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/provider"
)

const (
	fixtureEIPBandwidth      = 10
	fixtureAllocatedFirstIP  = "192.0.2.22"
	fixtureAllocatedSecondIP = "192.0.2.23"
	fixtureTencentEIPCharge  = "TRAFFIC_POSTPAID_BY_HOUR"
)

func (f *vpcCreationFixture) useElasticIP() {
	f.binding = newElasticIPBindingFixture(f.vendor)
	if f.vendor == provider.HuaweiECS {
		f.addressPool = "5_bgp"
		return
	}
	f.first, f.second = "eip-first", "eip-second"
}

func (f *vpcCreationFixture) observedAllocation() (id, address string, bandwidth int) {
	id, address, bandwidth = f.first, fixtureAllocatedFirstIP, fixtureEIPBandwidth
	if f.writes.Load() > 1 {
		id, address = f.second, fixtureAllocatedSecondIP
	}
	if f.badEvidence.Load() {
		bandwidth++
	}
	return
}

func (f *vpcCreationFixture) tencentElasticIP(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, action string, send func(any)) bool {
	t.Helper()
	switch action {
	case "AllocateAddresses":
		var data struct {
			AddressCount, InternetMaxBandwidthOut                                              int
			AddressName, AddressType, InternetServiceProvider, InternetChargeType, ClientToken string
			IsDedicatedAddressPool                                                             *bool
			Tags                                                                               []struct{ Key, Value string }
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || data.AddressCount != 1 || data.InternetMaxBandwidthOut != fixtureEIPBandwidth ||
			data.AddressName != fixtureVPCName || data.AddressType != "EIP" || data.InternetServiceProvider != "BGP" ||
			data.InternetChargeType != fixtureTencentEIPCharge || data.IsDedicatedAddressPool == nil || *data.IsDedicatedAddressPool ||
			len(data.Tags) != 1 || data.Tags[0].Key != fixtureVPCTag || !f.remember(t, r, data.Tags[0].Value, data.ClientToken) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]any{"RequestId": fixtureVPCReceipt, "AddressSet": []string{f.second}})
		}
	case "DescribeAddresses":
		var data struct {
			Offset, Limit int
			AddressIds    []string
			Filters       []struct {
				Name   string
				Values []string
			}
		}
		marker := f.marker.Load()
		if marker == nil || json.Unmarshal(body, &data) != nil || data.Offset != 0 || data.Limit != 2 {
			return false
		}
		if f.writes.Load() == 1 {
			if len(data.AddressIds) != 0 || len(data.Filters) != 1 || data.Filters[0].Name != "tag:"+fixtureVPCTag ||
				!slices.Equal(data.Filters[0].Values, []string{*marker}) {
				return false
			}
		} else if len(data.Filters) != 0 || !slices.Equal(data.AddressIds, []string{f.second}) {
			return false
		}
		rows := []any{}
		if f.visible.Load() {
			id, ip, bandwidth := f.observedAllocation()
			rows = append(rows, map[string]any{"AddressId": id, "AddressIp": ip, "AddressName": fixtureVPCName,
				"AddressStatus": "UNBIND", "AddressType": "EIP", "InternetChargeType": fixtureTencentEIPCharge, "Bandwidth": bandwidth,
				"TagSet": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}})
		}
		send(map[string]any{"AddressSet": rows, "TotalCount": len(rows)})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaElasticIP(t *testing.T, w http.ResponseWriter, r *http.Request, action string, send func(any)) bool {
	t.Helper()
	q := r.URL.Query()
	if r.Host != "vpc.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2016-04-28" || q.Get("RegionId") != f.region {
		return false
	}
	switch action {
	case "AllocateEipAddress":
		if len(q) != 9 || q.Get("Name") != fixtureVPCName || q.Get("Bandwidth") != strconv.Itoa(fixtureEIPBandwidth) ||
			q.Get("ISP") != "BGP" || q.Get("InstanceChargeType") != "PostPaid" || q.Get("InternetChargeType") != "PayByTraffic" ||
			q.Get("Tag.1.Key") != fixtureVPCTag || !f.remember(t, r, q.Get("Tag.1.Value"), q.Get("ClientToken")) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			send(map[string]string{"RequestId": fixtureVPCReceipt, "AllocationId": f.second})
		}
	case "DescribeEipAddresses":
		marker := f.marker.Load()
		if marker == nil || q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" {
			return false
		}
		if f.writes.Load() == 1 {
			if q.Has("AllocationId") || q.Get("Tag.1.Key") != fixtureVPCTag || q.Get("Tag.1.Value") != *marker {
				return false
			}
		} else if q.Has("Tag.1.Key") || q.Get("AllocationId") != f.second {
			return false
		}
		rows := []any{}
		if f.visible.Load() {
			id, ip, bandwidth := f.observedAllocation()
			rows = append(rows, map[string]any{"AllocationId": id, "IpAddress": ip, "Name": fixtureVPCName,
				"Status": "Available", "RegionId": f.region, "ChargeType": "PostPaid", "InternetChargeType": "PayByTraffic",
				"Bandwidth": strconv.Itoa(bandwidth), "Tags": map[string]any{"Tag": []map[string]string{{"Key": fixtureVPCTag, "Value": *marker}}}})
		}
		send(map[string]any{"PageNumber": 1, "PageSize": 10, "TotalCount": len(rows),
			"EipAddresses": map[string]any{"EipAddress": rows}})
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiElasticIP(t *testing.T, w http.ResponseWriter, r *http.Request, send func(any)) bool {
	t.Helper()
	path := "/v3/" + fixtureHuaweiProject + "/eip/publicips"
	if r.Method == http.MethodPost && r.URL.Path == path {
		var data struct {
			PublicIP struct {
				Alias, Description, Type string
				IPVersion                int    `json:"ip_version"`
				Pool                     string `json:"publicip_pool_name"`
				Bandwidth                struct {
					Size       int
					Name       string
					ChargeMode string `json:"charge_mode"`
					ShareType  string `json:"share_type"`
				}
			} `json:"publicip"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || r.Header.Get("Content-Type") != "application/json" {
			return false
		}
		row := data.PublicIP
		marker, found := strings.CutPrefix(row.Description, fixtureVPCTag+":")
		if !found || row.Type != "EIP" || row.IPVersion != 4 || row.Pool != f.addressPool ||
			row.Bandwidth.ChargeMode != "traffic" || row.Bandwidth.ShareType != "PER" ||
			row.Bandwidth.Size != fixtureEIPBandwidth || row.Alias != fixtureVPCName || !f.remember(t, r, marker, "") {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			send(map[string]any{"request_id": fixtureVPCReceipt, "publicip": map[string]string{"id": f.second, "project_id": fixtureHuaweiProject}})
		}
		return true
	}
	marker := f.marker.Load()
	if r.Method != http.MethodGet || marker == nil {
		return false
	}
	id, ip, bandwidth := f.observedAllocation()
	description := fixtureVPCTag + ":" + *marker
	row := map[string]any{"id": id, "project_id": fixtureHuaweiProject, "alias": fixtureVPCName, "description": description,
		"status": "DOWN", "type": "EIP", "ip_version": 4, "public_ip_address": ip, "publicip_pool_name": f.addressPool,
		"bandwidth": map[string]any{"size": bandwidth, "charge_mode": "traffic", "share_type": "PER"}}
	switch r.URL.Path {
	case path:
		q := r.URL.Query()
		if f.writes.Load() != 1 || q.Get("description") != description || q.Get("limit") != "2000" ||
			(q.Get("marker") != "" && q.Get("marker") != f.first) {
			return false
		}
		rows, next := []any{}, ""
		if f.visible.Load() && q.Get("marker") == "" {
			rows, next = append(rows, row), f.first
		}
		send(map[string]any{"publicips": rows, "page_info": map[string]any{"current_count": len(rows), "next_marker": next}})
	case path + "/" + f.second:
		if f.writes.Load() != 2 || r.URL.RawQuery != "" {
			return false
		}
		send(map[string]any{"publicip": row})
	default:
		return false
	}
	return true
}
