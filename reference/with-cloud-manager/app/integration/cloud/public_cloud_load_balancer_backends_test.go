package cloudtest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const (
	fixtureBackendIP              = "10.0.91.27"
	fixtureBackendServer          = "ins-secret-backend"
	fixtureUnmonitoredServer      = "ins-unmonitored-backend"
	fixtureAlibabaPrimaryGroup    = "rsp-pair"
	fixtureForeignBackendListener = "70000000-0000-4000-8000-000000000055"
	fixtureHuaweiMember           = "70000000-0000-4000-8000-000000000061"
	fixtureHuaweiMemberSecond     = "70000000-0000-4000-8000-000000000062"
)

func (f *loadBalancerConfigurationFixture) tencentBackends(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte) bool {
	var req struct {
		LoadBalancerId  string
		LoadBalancerIds []string
		ListenerIds     []string
		Filters         []struct {
			Name   string
			Values []string
		}
		Limit, Offset int
	}
	if json.Unmarshal(body, &req) != nil {
		return false
	}
	write := func(response any) bool { return sendNetworkFixture(t, w, map[string]any{"Response": response}) }
	switch r.Header.Get("X-TC-Action") {
	case "DescribeTargetGroupInstances":
		if f.classic || len(req.Filters) != 1 || req.Filters[0].Name != "TargetGroupId" ||
			!singleTarget(req.Filters[0].Values, f.group) || req.Limit != 50 || req.Offset != 0 {
			return false
		}
		return write(map[string]any{"TotalCount": 1, "RealCount": 1, "TargetGroupInstanceSet": []map[string]any{{
			"TargetGroupId": f.group, "InstanceId": fixtureBackendServer, "PrivateIpAddresses": []string{fixtureBackendIP},
			"Port": 8080, "Weight": 0,
		}}})
	case "DescribeTargetHealth":
		if f.classic || !singleTarget(req.LoadBalancerIds, f.id) {
			return false
		}
		if !f.allowBackendResponse(w) {
			return true
		}
		parent := f.id
		if f.backendFailure.Load() == 2 {
			parent = "lb-other-project"
		}
		return write(map[string]any{"LoadBalancers": []map[string]any{{
			"LoadBalancerId": parent, "Listeners": []map[string]any{{
				"ListenerId": f.listener, "Rules": []map[string]any{{
					"LocationId": "loc-fixture",
					"Targets": []map[string]any{{"TargetId": fixtureBackendServer, "IP": fixtureBackendIP, "Port": 8080,
						"HealthStatus": false, "HealthStatusDetail": "Dead"}},
				}},
			}},
		}}})
	case "DescribeTargets":
		if f.classic || req.LoadBalancerId != f.id || !slices.Equal(req.ListenerIds, []string{f.listener, "lbl-direct"}) {
			return false
		}
		rule := "loc-direct"
		if f.backendFailure.Load() == 3 {
			rule = "loc-other-project"
		}
		return write(map[string]any{"Listeners": []map[string]any{
			{"ListenerId": f.listener, "Protocol": "HTTPS", "Port": 443,
				"Rules": []map[string]any{
					{"LocationId": "loc-fixture", "Targets": []map[string]any{{"InstanceId": fixtureBackendServer, "Port": 8080, "Weight": 0}}},
					{"LocationId": rule, "Targets": []map[string]any{{"InstanceId": fixtureUnmonitoredServer, "Port": 8081, "Weight": 37}}},
				}},
			{"ListenerId": "lbl-direct", "Protocol": "TCP", "Port": 8443,
				"Targets": []map[string]any{{"InstanceId": fixtureUnmonitoredServer, "Port": 8443, "Weight": 11}}},
		}})
	case "DescribeClassicalLBHealthStatus":
		if !f.classic || req.LoadBalancerId != f.id {
			return false
		}
		if !f.allowBackendResponse(w) {
			return true
		}
		port := 443
		if f.backendFailure.Load() == 2 {
			port = 80
		}
		return write(map[string]any{"HealthList": []map[string]any{{
			"ListenerPort": port, "Protocol": "TCP", "Port": 8080, "IP": fixtureBackendIP, "HealthStatus": 0,
		}}})
	case "DescribeClassicalLBTargets":
		if !f.classic || req.LoadBalancerId != f.id {
			return false
		}
		return write(map[string]any{"Targets": []map[string]any{
			{"InstanceId": fixtureBackendServer, "PrivateIpAddresses": []string{fixtureBackendIP}, "Weight": 0},
			{"InstanceId": fixtureUnmonitoredServer, "PrivateIpAddresses": []string{"10.0.91.28"}, "Weight": 37},
		}})
	default:
		return false
	}
}

func (f *loadBalancerConfigurationFixture) alibabaBackends(t *testing.T, w http.ResponseWriter, action string, q url.Values) bool {
	if action == "ListServerGroupServers" {
		group := q.Get("ServerGroupId")
		if (group != f.group && (f.product != "alibaba-alb" || group != "sgp-mirror")) || q.Get("MaxResults") != "50" {
			return false
		}
		servers := []map[string]any{{
			"ServerGroupId": group, "ServerId": fixtureBackendServer, "ServerIp": fixtureBackendIP, "Port": 8080, "Weight": 0,
		}}
		cursor := ""
		if f.product == "alibaba-nlb" {
			const next = "members+second=="
			switch q.Get("NextToken") {
			case "":
				cursor = next
			case next:
				servers = servers[:0]
			default:
				return false
			}
		}
		return sendNetworkFixture(t, w, map[string]any{"NextToken": cursor, "Servers": servers})
	}
	if action != "GetListenerHealthStatus" {
		return false
	}
	nlb := f.product == "alibaba-nlb"
	if nlb {
		if q.Has("IncludeRule") || q.Has("MaxResults") {
			return false
		}
	} else if q.Get("IncludeRule") != "true" || q.Get("MaxResults") != "30" {
		return false
	}
	listener := q.Get("ListenerId")
	if listener == "lsn-second" && !nlb {
		return sendNetworkFixture(t, w, map[string]any{"ListenerHealthStatus": []any{}, "RuleHealthStatus": []any{}})
	}
	if listener != f.listener {
		return false
	}
	if !f.allowBackendResponse(w) {
		return true
	}
	port := 443
	if nlb {
		port = 445
		base, _, _ := strings.Cut(listener, "@")
		listener = base + "@" + strconv.Itoa(port)
	}
	group := f.group
	if f.backendFailure.Load() == 2 {
		group = "sgp-other-project"
	}
	healthGroup := func(id, status string) map[string]any {
		return map[string]any{"ServerGroupId": id, "NonNormalServers": []map[string]any{{
			"Status": status, "Port": 8080, "ServerId": fixtureBackendServer, "ServerIp": fixtureBackendIP,
		}}}
	}
	response := map[string]any{"ListenerHealthStatus": []map[string]any{{
		"ListenerId": listener, "ListenerPort": port, "ServerGroupInfos": []map[string]any{healthGroup(group, "Unhealthy")},
	}}}
	if !nlb {
		response["RuleHealthStatus"] = []map[string]any{{"RuleId": "rule-fixture",
			"ServerGroupInfos": []map[string]any{healthGroup("sgp-mirror", "Initial")},
		}}
	} else if f.backendFailure.Load() == 3 {
		response["NextToken"] = "health-cannot-be-paginated"
	}
	return sendNetworkFixture(t, w, response)
}

func (f *loadBalancerConfigurationFixture) alibabaClassicBackends(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeVServerGroupAttribute":
		if q.Get("VServerGroupId") != f.group || q.Has("LoadBalancerId") {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"VServerGroupId": f.group, "BackendServers": map[string]any{
			"BackendServer": []map[string]any{{"ServerId": fixtureBackendServer, "ServerIp": fixtureBackendIP, "Port": 8080, "Weight": 0}},
		}})
	case "DescribeHealthStatus":
		if q.Get("LoadBalancerId") != f.id || q.Get("ListenerProtocol") != "tcp" {
			return false
		}
		if q.Get("ListenerPort") == "8443" {
			return sendNetworkFixture(t, w, map[string]any{"BackendServers": map[string]any{"BackendServer": []any{}}})
		}
		if q.Get("ListenerPort") != "443" {
			return false
		}
		if !f.allowBackendResponse(w) {
			return true
		}
		port := 443
		if f.backendFailure.Load() == 2 {
			port = 80
		}
		return sendNetworkFixture(t, w, map[string]any{"BackendServers": map[string]any{
			"BackendServer": []map[string]any{{"ServerId": fixtureBackendServer, "ServerIp": fixtureBackendIP, "Port": 8080,
				"ListenerPort": port, "ServerHealthStatus": "abnormal"}},
		}})
	case "DescribeMasterSlaveServerGroups":
		if q.Get("LoadBalancerId") != f.id || q.Get("IncludeListener") != "true" {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"MasterSlaveServerGroups": map[string]any{
			"MasterSlaveServerGroup": []map[string]any{{
				"MasterSlaveServerGroupId": fixtureAlibabaPrimaryGroup, "MasterSlaveServerGroupName": "Pair",
				"AssociatedObjects": map[string]any{"Listeners": map[string]any{"Listener": []map[string]any{{"Port": 8443, "Protocol": "tcp"}}}},
			}},
		}})
	case "DescribeMasterSlaveServerGroupAttribute":
		if q.Get("MasterSlaveServerGroupId") != fixtureAlibabaPrimaryGroup || q.Has("LoadBalancerId") {
			return false
		}
		id, secondary := fixtureAlibabaPrimaryGroup, "Slave"
		switch f.backendFailure.Load() {
		case 3:
			secondary = "Master"
		case 4:
			id = "rsp-other-project"
		}
		return sendNetworkFixture(t, w, map[string]any{"MasterSlaveServerGroupId": id, "MasterSlaveBackendServers": map[string]any{
			"MasterSlaveBackendServers": []map[string]any{
				{"ServerId": fixtureBackendServer, "Port": "8080", "Weight": "91", "ServerType": "Master"},
				{"ServerId": fixtureUnmonitoredServer, "Port": 8081, "Weight": 0, "ServerType": secondary},
			},
		}})
	default:
		return false
	}
}

func validateAlibabaClassicGroups(t *testing.T, configuration *inventory.LoadBalancerConfiguration, id string) {
	t.Helper()
	defaultID := id + ".default"
	for _, expected := range []provider.LoadBalancerTargetGroup{
		{ID: defaultID, Kind: provider.LoadBalancerDefaultGroup},
		{ID: fixtureAlibabaPrimaryGroup, Kind: provider.LoadBalancerPrimarySecondaryGroup},
		{ID: "rsp-fixture", Kind: provider.LoadBalancerVirtualGroup},
	} {
		if !slices.ContainsFunc(configuration.TargetGroups, func(group provider.LoadBalancerTargetGroup) bool {
			return group.ID == expected.ID && group.Kind == expected.Kind
		}) {
			t.Fatalf("missing target group type: %+v", expected)
		}
	}
	if !slices.ContainsFunc(configuration.Listeners, func(listener provider.LoadBalancerListener) bool {
		return listener.Port == 8443 && slices.Equal(listener.TargetGroupIDs, []string{fixtureAlibabaPrimaryGroup})
	}) {
		t.Fatalf("missing explicit primary group binding: %+v", configuration.Listeners)
	}
	if !slices.ContainsFunc(configuration.Backends.Members, func(member provider.LoadBalancerBackendMember) bool {
		return member.GroupID == defaultID && member.Port == 0 && member.Weight != nil && *member.Weight == 37
	}) {
		t.Fatalf("missing unmonitored default member: %+v", configuration.Backends.Members)
	}
	for _, role := range []string{"primary", "secondary"} {
		if !slices.ContainsFunc(configuration.Backends.Members, func(member provider.LoadBalancerBackendMember) bool {
			return member.GroupID == fixtureAlibabaPrimaryGroup && member.Role == role && member.Port > 0
		}) {
			t.Fatalf("missing member role %s: %+v", role, configuration.Backends.Members)
		}
	}
}

func validateTencentDirectGroups(t *testing.T, configuration *inventory.LoadBalancerConfiguration) {
	t.Helper()
	for _, id := range []string{"lbl-fixture.loc-direct.direct", "lbl-direct.direct"} {
		if !slices.ContainsFunc(configuration.TargetGroups, func(group provider.LoadBalancerTargetGroup) bool {
			return group.ID == id && group.Kind == provider.LoadBalancerDirectGroup
		}) || !slices.ContainsFunc(configuration.Backends.Members, func(member provider.LoadBalancerBackendMember) bool {
			return member.GroupID == id && member.Port != 0
		}) {
			t.Fatalf("missing direct membership: %s", id)
		}
		if !slices.ContainsFunc(configuration.Listeners, func(listener provider.LoadBalancerListener) bool {
			return strings.HasPrefix(id, listener.ID+".") && slices.Contains(listener.TargetGroupIDs, id)
		}) {
			t.Fatalf("missing scoped listener association: %s", id)
		}
	}
}

func (f *loadBalancerConfigurationFixture) huaweiBackends(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if q.Get("limit") != "50" || q.Has("loadbalancer_id") {
		return false
	}
	id, marker, project := fixtureHuaweiMember, fixtureHuaweiMember, fixtureHuaweiProject
	if q.Get("marker") != "" {
		if q.Get("marker") != marker {
			return false
		}
		if !f.allowBackendResponse(w) {
			return true
		}
		id, marker = fixtureHuaweiMemberSecond, ""
		if f.backendFailure.Load() == 2 {
			project = "00000000000000000000000000000000"
		}
	}
	return sendNetworkFixture(t, w, map[string]any{"members": []map[string]any{{
		"id": id, "project_id": project, "address": fixtureBackendIP, "instance_id": fixtureBackendServer,
		"protocol_port": 8080, "weight": 0, "status": []map[string]string{
			{"listener_id": f.listener, "operating_status": "OFFLINE"}, {"listener_id": fixtureForeignBackendListener, "operating_status": "ONLINE"},
		},
	}}, "page_info": map[string]any{"current_count": 1, "next_marker": marker}})
}

func (f *loadBalancerConfigurationFixture) allowBackendResponse(w http.ResponseWriter) bool {
	if f.backendFailure.Load() != 1 {
		return true
	}
	w.Header().Set("Retry-After", "2")
	w.WriteHeader(http.StatusTooManyRequests)
	return false
}

func expectBackendState(t *testing.T, data *provider.LoadBalancerBackendSnapshot, listener string, frontend int, rule, health string) {
	t.Helper()
	for _, check := range data.Checks {
		if check.ListenerID == listener && check.ListenerPort == frontend && check.RuleID == rule && check.Status == health {
			return
		}
	}
	t.Fatalf("missing backend check at %s/%d/%s: %+v", listener, frontend, rule, data.Checks)
}
