package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type publicCloudResourceFixture struct {
	provider, region, account, project string
	key, secret, session, token        string
}

func (f publicCloudResourceFixture) id(kind provider.Kind) string {
	if kind == provider.Zone {
		switch f.provider {
		case provider.HuaweiECS:
			return f.region + "a"
		case provider.AlibabaECS:
			return f.region + "-a"
		default:
			return f.region + "-1"
		}
	}
	if f.provider == provider.HuaweiECS {
		switch kind {
		case provider.Network:
			return "10000000-0000-4000-8000-000000000001"
		case provider.Subnet:
			return "20000000-0000-4000-8000-000000000001"
		case provider.Volume:
			return "40000000-0000-4000-8000-000000000001"
		case provider.Image:
			return "50000000-0000-4000-8000-000000000001"
		default:
			return "30000000-0000-4000-8000-000000000001"
		}
	}
	switch kind {
	case provider.Network:
		return "vpc-1"
	case provider.Subnet:
		if f.provider == provider.AlibabaECS {
			return "vsw-1"
		}
		return "subnet-1"
	case provider.Volume:
		if f.provider == provider.AlibabaECS {
			return "d-1"
		}
		return "disk-1"
	case provider.Image:
		if f.provider == provider.AlibabaECS {
			return "m-1"
		}
		return "img-1"
	default:
		return "sg-1"
	}
}

func (f publicCloudResourceFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request, wrongScope bool) bool {
	t.Helper()
	fail := func() {
		t.Errorf("incorrect network inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
	var result any
	switch {
	case r.Host == "vpc.tencentcloudapi.com":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Version") != "2017-03-12" {
			fail()
			return true
		}
		var page struct{ Offset, Limit string }
		action := r.Header.Get("X-TC-Action")
		if json.Unmarshal(body, &page) != nil || (page.Limit != "100" && (action != "DescribeSecurityGroups" || page.Limit != "1")) ||
			(page.Offset != "0" && page.Offset != "1") {
			fail()
			return true
		}
		region := f.region
		if wrongScope {
			region = "ap-beijing"
		}
		response := map[string]any{"TotalCount": 1}
		switch r.Header.Get("X-TC-Action") {
		case "DescribeVpcs":
			response["TotalCount"] = 2
			id := "vpc-1"
			if page.Offset == "1" {
				id = "vpc-2"
			}
			response["VpcSet"] = []any{map[string]any{
				"VpcId": id, "VpcName": "fixture-network",
				"TagSet": []any{map[string]string{"Key": "env", "Value": "test"}},
			}}
		case "DescribeSubnets":
			response["SubnetSet"] = []any{map[string]string{
				"SubnetId": f.id(provider.Subnet), "SubnetName": "fixture-subnet", "VpcId": f.id(provider.Network), "Zone": f.region + "-1",
			}}
		case "DescribeSecurityGroups":
			response["TotalCount"] = 2
			first := map[string]string{"SecurityGroupId": f.id(provider.SecurityGroup), "SecurityGroupName": "fixture-group", "Region": region}
			second := map[string]string{"SecurityGroupId": "sg-2", "SecurityGroupName": "fixture-second-group", "Region": region}
			switch {
			case page.Offset == "1":
				response["SecurityGroupSet"] = []any{second}
			case page.Limit == "1":
				response["SecurityGroupSet"] = []any{first}
			default:
				response["SecurityGroupSet"] = []any{first, second}
			}
		default:
			fail()
			return true
		}
		result = map[string]any{"Response": response}
	case r.Host == "vpc.aliyuncs.com":
		q := r.URL.Query()
		number, err := strconv.Atoi(q.Get("PageNumber"))
		if err != nil || (number != 1 && number != 2) || q.Get("PageSize") != "50" || q.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2016-04-28" {
			fail()
			return true
		}
		owner := json.Number(f.account)
		if wrongScope {
			owner = "1000000000000007"
		}
		response := map[string]any{"PageNumber": number, "PageSize": 50, "TotalCount": 1}
		switch r.Header.Get("X-ACS-Action") {
		case "DescribeVpcs":
			if q.Get("VpcOwnerId") != f.account {
				fail()
				return true
			}
			first := (number-1)*50 + 1
			rows := []any{}
			for i := first; i <= min(first+49, 51); i++ {
				rows = append(rows, map[string]any{
					"VpcId": fmt.Sprintf("vpc-%d", i), "VpcName": "fixture-network", "OwnerId": owner,
					"RegionId": f.region, "Status": "Available", "Tags": map[string]any{"Tag": []any{map[string]string{"Key": "env", "Value": "test"}}},
				})
			}
			response["TotalCount"] = 51
			response["Vpcs"] = map[string]any{"Vpc": rows}
		case "DescribeVSwitches":
			if q.Get("VSwitchOwnerId") != f.account {
				fail()
				return true
			}
			response["VSwitches"] = map[string]any{"VSwitch": []any{map[string]any{
				"VSwitchId": f.id(provider.Subnet), "VSwitchName": "fixture-subnet", "VpcId": f.id(provider.Network),
				"OwnerId": owner, "Status": "Available", "ZoneId": f.region + "-a",
			}}}
		default:
			fail()
			return true
		}
		result = response
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeSecurityGroups":
		q := r.URL.Query()
		const token = "security+group/page=2"
		cursor := q.Get("NextToken")
		if (cursor != "" && cursor != token) || (q.Get("MaxResults") != "100" && q.Get("MaxResults") != "1") || q.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			fail()
			return true
		}
		id, next := f.id(provider.SecurityGroup), token
		if cursor != "" {
			id, next = "sg-2", ""
		}
		result = map[string]any{
			"RegionId": f.region, "NextToken": next,
			"SecurityGroups": map[string]any{"SecurityGroup": []any{map[string]string{
				"SecurityGroupId": id, "SecurityGroupName": "fixture-group", "VpcId": f.id(provider.Network),
			}}},
		}
	case r.Host == "vpc.cn-north-4.myhuaweicloud.com":
		query := r.URL.Query()
		cursor := query.Get("marker")
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != f.token ||
			query.Get("limit") != "100" || (cursor != "" && cursor != f.id(provider.Network)) {
			fail()
			return true
		}
		var field string
		project := f.project
		if wrongScope {
			project = "00000000000000000000000000000000"
		}
		page := map[string]any{"current_count": 1}
		row := map[string]any{"project_id": project}
		switch r.URL.Path {
		case "/v3/" + f.project + "/vpc/vpcs":
			field = "vpcs"
			row["id"], row["name"], row["status"] = f.id(provider.Network), "fixture-network", "ACTIVE"
			row["tags"] = []any{map[string]string{"key": "env", "value": "test"}}
			if cursor == "" {
				page["next_marker"] = row["id"]
			} else {
				row["id"] = "10000000-0000-4000-8000-000000000002"
			}
		case "/v3/" + f.project + "/vpc/virsubnets":
			field = "virsubnets"
			row["id"], row["name"], row["status"] = f.id(provider.Subnet), "fixture-subnet", "ACTIVE"
			row["vpc_id"], row["zone_id"] = f.id(provider.Network), f.region+"a"
		case "/v3/" + f.project + "/vpc/security-groups":
			field = "security_groups"
			row["id"], row["name"] = f.id(provider.SecurityGroup), "fixture-group"
		default:
			fail()
			return true
		}
		result = map[string]any{field: []any{row}, "page_info": page}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Error(err)
	}
	return true
}

func (f publicCloudResourceFixture) checkTopology(t *testing.T, kind provider.Kind, nodes []inventory.TopologyNode) {
	t.Helper()
	checkLink := func(node inventory.TopologyNode, kind provider.Kind, relation string) {
		if !slices.ContainsFunc(node.Links, func(link inventory.TopologyLink) bool {
			return link.Kind == kind && link.ID == f.id(kind) && link.Relation == relation && link.Name != ""
		}) {
			t.Errorf("%s %s missing %s link to %s: %+v", node.Asset.Kind, node.Asset.ID, relation, f.id(kind), node.Links)
		}
	}
	checkCount := func(count int) {
		if len(nodes) != count {
			t.Errorf("unexpected %s count %d, want %d", kind, len(nodes), count)
		}
	}
	switch kind {
	case provider.Network:
		if f.provider == provider.AlibabaECS {
			checkCount(50)
		} else {
			checkCount(2)
		}
	case provider.Subnet:
		checkCount(1)
		checkLink(nodes[0], provider.Network, "in_network")
		checkLink(nodes[0], provider.Zone, "in_zone")
	case provider.SecurityGroup:
		switch f.provider {
		case provider.AlibabaECS:
			checkCount(2)
			for _, node := range nodes {
				checkLink(node, provider.Network, "in_network")
			}
		case provider.TencentCVM:
			checkCount(2)
		default:
			checkCount(1)
		}
	case provider.SecurityGroupRule:
		checkCount(2)
		for _, node := range nodes {
			if len(node.Links) != 1 || node.Asset.ParentID != node.Links[0].ID ||
				node.Links[0].Kind != provider.SecurityGroup || node.Links[0].Relation != "in_security_group" {
				t.Errorf("incorrect rule parent: %+v", node)
			}
		}
		f.checkRuleFields(t, nodes)
	case provider.Volume:
		checkCount(2)
		for _, node := range nodes {
			if node.Asset.ID != f.id(provider.Volume) {
				if len(node.Links) != 0 {
					t.Errorf("unattached disk %s has links: %+v", node.Asset.ID, node.Links)
				}
				continue
			}
			checkLink(node, provider.Zone, "in_zone")
			attachments := 0
			for _, link := range node.Links {
				if link.Kind == provider.Instance && link.Relation == "attached_to" && link.Name != "" {
					attachments++
				}
			}
			if attachments != 2 || len(node.Links) != 3 {
				t.Errorf("disk %s has invalid attachments: %+v", node.Asset.ID, node.Links)
			}
		}
	case provider.Image:
		if f.provider == provider.AlibabaECS {
			checkCount(50)
		} else {
			checkCount(2)
		}
	case provider.Zone:
		checkCount(1)
		if nodes[0].Asset.ID != f.id(provider.Zone) {
			t.Errorf("unexpected zone code %s", nodes[0].Asset.ID)
		}
	case provider.Instance:
		checkCount(2)
		for _, node := range nodes {
			checkLink(node, provider.Network, "in_network")
			checkLink(node, provider.SecurityGroup, "uses_security_group")
			checkLink(node, provider.Image, "uses_image")
			checkLink(node, provider.Zone, "in_zone")
			if f.provider != provider.HuaweiECS {
				checkLink(node, provider.Subnet, "in_subnet")
			}
			if f.provider == provider.TencentCVM {
				checkLink(node, provider.Volume, "uses_volume")
			}
		}
	}
}
