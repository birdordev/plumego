package kvmtest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMHostDHCPPlan(t *testing.T) {
	_, first, second := prepareKVM(t)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/networks", "", "", fixtureIPv4Pool), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	path := fixtureVMHostPath + "/dhcp-plan/app"
	query := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	var pool kvm.IPv4Pool
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", path, "", "", query), 200, &pool)
	if pool.Site != "site-a" || pool.IPv4PoolSpec != fixtureIPv4Pool {
		t.Fatalf("host received wrong pool: %+v", pool)
	}
	for _, actor := range []string{fixtureKVMAdmin, fixtureKVMGuest, fixtureKVMReplacement} {
		expectKVM(t, kvmRequest(t, first, actor, "POST", path, "", "", query), 403, nil)
	}
	wrongHost := query
	wrongHost.UUID = fixtureNetworkUUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", path, "", "", wrongHost), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", strings.Replace(path, "/compute/", "/other/", 1), "", "", query), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+"/dhcp-plan/backup", "", "", query), 403, nil)
	unassigned := fixtureIPv4Pool
	unassigned.Network, unassigned.Subnet, unassigned.Gateway = "isolated", "10.78.0.0/29", "10.78.0.1"
	unassigned.First, unassigned.Last = "10.78.0.2", "10.78.0.4"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/networks", "", "", unassigned), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+"/dhcp-plan/isolated", "", "", query), 403, nil)

	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 200, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", path, "", "", query), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "POST", path, "", "", query), 412, nil)
	query.Generation = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "POST", path, "", "", query), 200, nil)
}

func TestKVMDHCPCLIPaths(t *testing.T) {
	base, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"plan-network-dhcp", "apply-network-dhcp"} {
		args := []string{command, "lab", "compute", "site-a", "host-one", "1", "app", fixtureNetworkUUID}
		plan, err := kvmapp.PlanKVMRequest(base, args)
		if err != nil || plan.Target.String() != base.String()+fixtureVMHostPath || plan.Method != http.MethodGet {
			t.Fatalf("invalid DHCP CLI plan: %+v %v", plan, err)
		}
		args[7] = "app"
		if _, err := kvmapp.PlanKVMRequest(base, args); err == nil {
			t.Fatal("DHCP command accepted a name in place of the UUID")
		}
	}
}
