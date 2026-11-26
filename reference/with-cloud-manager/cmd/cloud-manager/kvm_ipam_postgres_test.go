package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

var fixtureIPv4Pool = kvm.IPv4PoolSpec{Network: "app", Subnet: "10.77.0.0/29",
	Gateway: "10.77.0.1", First: "10.77.0.2", Last: "10.77.0.4"}

func TestAcceptanceKVMIPv4Pool(t *testing.T) {
	db, first, second := prepareKVM(t)
	pools := fixtureKVMSite + "/networks"
	placements := fixtureKVMSite + "/placements"
	for _, actor := range []string{fixtureKVMOperator, fixtureKVMGuest, fixtureKVMAgent} {
		expectKVM(t, kvmRequest(t, first, actor, "POST", pools, "", "", fixtureIPv4Pool), 403, nil)
	}
	for _, invalid := range []kvm.IPv4PoolSpec{
		{Network: "app", Subnet: "10.77.0.1/29", Gateway: "10.77.0.1", First: "10.77.0.2", Last: "10.77.0.4"},
		{Network: "app", Subnet: "10.77.0.0/29", Gateway: "10.77.0.3", First: "10.77.0.2", Last: "10.77.0.4"},
		{Network: "app", Subnet: "10.77.0.0/29", Gateway: "10.77.0.1", First: "10.77.0.2", Last: "10.77.0.7"},
	} {
		expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", pools, "", "", invalid), 400, nil)
	}

	host := fixtureKVMHost()
	report := fixtureKVMReport()
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 200, nil)
	host.ID, host.UUID, host.Agent = "host-two", "feaa8a19-4765-4bb4-a383-3a1011ba0002", "replacement-agent"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", fixtureKVMSite+"/hosts/host-two/capacity", "", "", report), 200, nil)

	var legacy kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", placements, "", "before-ipam", fixtureKVMPlacement()), 201, &legacy)
	if legacy.Address != "" || legacy.MAC != "" {
		t.Fatalf("legacy placement received IPAM fields: %+v", legacy)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", pools, "", "", fixtureIPv4Pool), 409, nil)
	age := func(p kvm.Placement) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements
			SET created_at=clock_timestamp()-interval '3 minutes',expires_at=clock_timestamp()-interval '2 minutes'
			WHERE placement_id=$1`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	age(legacy)
	var pool kvm.IPv4Pool
	response := kvmRequest(t, second, fixtureKVMAdmin, "POST", pools, "", "", fixtureIPv4Pool)
	expectKVM(t, response, 201, &pool)
	if pool.IPv4PoolSpec != fixtureIPv4Pool || !strings.HasSuffix(response.Header().Get("Location"), "/networks/app") {
		t.Fatalf("pool receipt: %+v %v", pool, response.Header())
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", pools+"/app", "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", strings.Replace(pools, "/compute/", "/other/", 1)+"/app", "", "", nil), 403, nil)
	var pinned kvm.IPv4Pool
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", pools+"/app", "", "", nil), 200, &pinned)
	if pinned != pool {
		t.Fatalf("pool changed across replicas: %+v", pinned)
	}
	overlap := fixtureIPv4Pool
	overlap.Network = "backup"
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", pools, "", "", overlap), 409, nil)

	var wg sync.WaitGroup
	type attempt struct {
		result *httptest.ResponseRecorder
		input  kvm.PlacementInput
		key    string
	}
	attempts := make([]attempt, 12)
	for index := range attempts {
		input := fixtureKVMPlacement()
		handler := first
		input.RequiredHost = "host-two"
		if index%2 == 0 {
			handler = second
			input.RequiredHost = "host-one"
		}
		key := "ip-race-" + string(rune('a'+index))
		wg.Go(func() {
			r := kvmRequest(t, handler, fixtureKVMOperator, "POST", placements, "", key, input)
			attempts[index] = attempt{result: r, input: input, key: key}
		})
	}
	wg.Wait()
	var expired kvm.Placement
	var expiredAttempt attempt
	seen := make(map[string]bool)
	for _, a := range attempts {
		if a.result.Code == http.StatusServiceUnavailable {
			// 截止时间内未收到收据时，按原幂等键核对受理结果。
			a.result = kvmRequest(t, second, fixtureKVMOperator, "POST", placements, "", a.key, a.input)
		}
		if a.result.Code == 409 {
			continue
		}
		var p kvm.Placement
		expectKVM(t, a.result, 201, &p)
		if p.Address == "" || p.MAC != kvm.IPv4MAC(p.Address) || seen[p.Address] || p.Host != a.input.RequiredHost {
			t.Fatalf("duplicate or invalid allocation: %+v", p)
		}
		seen[p.Address] = true
		expired, expiredAttempt = p, a
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique addresses: %v", seen)
	}
	var committed int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_placements
		WHERE idempotency_key LIKE 'ip-race-%'`).Scan(&committed); err != nil || committed != 3 {
		t.Fatalf("address contention or replay committed %d placements: %v", committed, err)
	}
	age(expired)
	input := fixtureKVMPlacement()
	input.IPv4 = expired.Address
	input.RequiredHost = "host-one"
	if expired.Host == "host-one" {
		input.RequiredHost = "host-two"
	}
	var slot kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", placements, "", "cross-host-reclaim", input), 201, &slot)
	if slot.Address != expired.Address || slot.Host == expired.Host {
		t.Fatalf("expired address was not reclaimed by another host: %+v", slot)
	}
	var history kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", placements, "", expiredAttempt.key, expiredAttempt.input), 201, &history)
	if history.ID != expired.ID || history.Address != expired.Address || history.State != "expired" {
		t.Fatalf("expired historical replay is not stable: %+v", history)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", placements, "", "duplicated-fixed-ip", input), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "DELETE", placements+"/"+slot.ID, "", "", nil), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", placements, "", "released-address", input), 201, nil)
}
