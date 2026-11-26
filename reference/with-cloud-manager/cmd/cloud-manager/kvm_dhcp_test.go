package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/kvm"
)

const fixtureNetworkUUID = "60460b62-3ab1-4bbe-bec1-275f8cf80001"
const fixtureEmptyDHCPLeases = " Expiry Time  MAC address  Protocol  IP address  Hostname  Client ID or DUID\n-------------------------------\n"

type fixtureDHCPNetwork struct {
	live, config     map[string]kvmDHCPHost
	allow            bool
	reverse          bool
	lostReply        bool
	partial          bool
	writes           int
	rangeXML, leases string
	configUUID       string
}

func (f *fixtureDHCPNetwork) command(t *testing.T) (virshReader, virshReader) {
	t.Helper()
	fallback := fixtureKVMProbe(t, fixtureKVMUUID, "", 4096).read
	read := func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "net-list":
			if slices.Equal(args, []string{"net-list", "--uuid", "--persistent"}) {
				return []byte(fixtureNetworkUUID + "\n"), nil
			}
		case "net-dhcp-leases":
			if slices.Equal(args, []string{"net-dhcp-leases", fixtureNetworkUUID}) {
				return []byte(fixtureEmptyDHCPLeases + f.leases), nil
			}
		case "net-dumpxml":
			if len(args) >= 3 && args[1] == "--network" && args[2] == fixtureNetworkUUID {
				hosts := f.live
				inactive := slices.Contains(args, "--inactive")
				uuid := fixtureNetworkUUID
				if inactive {
					hosts = f.config
					if f.configUUID != "" {
						uuid = f.configUUID
					}
				}
				var buf strings.Builder
				buf.WriteString("<network><name>app</name><uuid>" + uuid + "</uuid><forward mode='nat'/>")
				if f.reverse && !inactive {
					buf.WriteString("<ip family='ipv6' address='fd00::1' prefix='64'/>")
				}
				buf.WriteString("<ip address='10.77.0.1' prefix='29'><dhcp>" + f.rangeXML)
				for _, host := range hosts {
					data, err := xml.Marshal(host)
					if err != nil {
						return nil, err
					}
					buf.Write(data)
				}
				buf.WriteString("</dhcp></ip></network>")
				return []byte(buf.String()), nil
			}
		}
		return fallback(ctx, args...)
	}
	write := func(_ context.Context, args ...string) ([]byte, error) {
		t.Helper()
		if !f.allow || len(args) < 8 || !slices.Equal(args[:4], []string{"net-update", fixtureNetworkUUID, "add-last", "ip-dhcp-host"}) ||
			args[5] != "--parent-index" {
			t.Errorf("unexpected or unauthorized libvirt command: %v", args)
			return nil, errors.New("unexpected command")
		}
		var host kvmDHCPHost
		if err := xml.Unmarshal([]byte(args[4]), &host); err != nil || host.MAC != kvm.IPv4MAC(host.IP) ||
			host.Name != "cmp-ip-"+strings.ReplaceAll(host.IP, ".", "-") {
			t.Errorf("invalid DHCP entry: %+v %v", host, err)
			return nil, errors.New("invalid DHCP entry")
		}
		live, config := slices.Contains(args[7:], "--live"), slices.Contains(args[7:], "--config")
		expectedIndex := 0
		if live && f.reverse {
			expectedIndex = 1
		}
		if args[6] != strconv.Itoa(expectedIndex) || (live && config && f.reverse) ||
			(live && f.live[host.IP].IP != "") || (config && f.config[host.IP].IP != "") {
			t.Errorf("unsafe update flags or duplicate addition: %v", args)
			return nil, errors.New("conflicting command")
		}
		f.writes++
		if live {
			f.live[host.IP] = host
		}
		if config && !f.partial {
			f.config[host.IP] = host
		}
		if f.lostReply {
			f.lostReply = false
			return nil, errors.New("fixture lost net-update reply")
		}
		return nil, nil
	}
	return read, write
}

func newKVMDHCPFixtureAPI(t *testing.T, denyAfter int) kvmVMAPI {
	t.Helper()
	var queries atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureKVMAgent {
			t.Errorf("missing bound agent authorization")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			writeCloudFixtureJSON(t, w, map[string]any{"data": kvm.Host{HostSpec: fixtureKVMHost(), Site: "site-a", Generation: 1}})
		case http.MethodPost:
			var agent kvm.AgentIdentity
			if r.URL.Path != fixtureVMHostPath+"/dhcp-plan/app" || json.NewDecoder(r.Body).Decode(&agent) != nil ||
				agent != (kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}) {
				t.Errorf("invalid host DHCP plan request: %s %+v", r.URL.Path, agent)
			}
			count := queries.Add(1)
			if denyAfter > 0 && count > int32(denyAfter) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeCloudFixtureJSON(t, w, map[string]any{"data": kvm.IPv4Pool{IPv4PoolSpec: fixtureIPv4Pool, Site: "site-a"}})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return kvmVMAPI{target: base.JoinPath(fixtureVMHostPath), token: fixtureKVMAgent, transport: server.Client().Transport}
}

func runFixtureDHCP(t *testing.T, api kvmVMAPI, fixture *fixtureDHCPNetwork, apply bool) (kvmDHCPReport, error) {
	t.Helper()
	var output bytes.Buffer
	read, write := fixture.command(t)
	err := runKVMDHCP(t.Context(), api, kvmDHCPTarget{host: "host-one", generation: "1", network: "app", uuid: fixtureNetworkUUID, apply: apply}, &output, read, write)
	var report kvmDHCPReport
	if err == nil {
		if err := json.Unmarshal(output.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
	}
	return report, err
}

func TestKVMDHCPApply(t *testing.T) {
	api := newKVMDHCPFixtureAPI(t, 0)
	manual := kvmDHCPHost{IP: "10.77.0.2", MAC: kvm.IPv4MAC("10.77.0.2"), Name: "manual"}
	unrelated := kvmDHCPHost{IP: "10.77.0.5", MAC: "52:54:00:01:02:03", Name: "external"}
	fixture := &fixtureDHCPNetwork{
		live: map[string]kvmDHCPHost{unrelated.IP: unrelated}, config: map[string]kvmDHCPHost{manual.IP: manual},
		reverse: true, lostReply: true,
	}
	preview, err := runFixtureDHCP(t, api, fixture, false)
	if err != nil || preview.Remaining != 3 || len(preview.Next) != 3 || !preview.Next[0].Live || preview.Next[0].Config ||
		fixture.writes != 0 {
		t.Fatalf("wrong preview: %+v %v", preview, err)
	}
	fixture.allow = true
	recovered, err := runFixtureDHCP(t, api, fixture, true)
	if err != nil || recovered.Remaining != 2 || recovered.VerifiedUpdates != 1 || !recovered.RecoveredReply || fixture.writes != 1 {
		t.Fatalf("lost reply was not reconciled: %+v %v", recovered, err)
	}
	applied, err := runFixtureDHCP(t, api, fixture, true)
	if err != nil || applied.Remaining != 0 || applied.VerifiedUpdates != 4 || fixture.writes != 5 ||
		fixture.config[manual.IP] != manual || fixture.live[unrelated.IP] != unrelated {
		t.Fatalf("wrong applied state: %+v %v", applied, err)
	}
	again, err := runFixtureDHCP(t, api, fixture, true)
	if err != nil || again.Remaining != 0 || again.VerifiedUpdates != 0 || len(again.Next) != 0 || fixture.writes != 5 {
		t.Fatalf("complete plan is not idempotent: %+v %v", again, err)
	}
}

func TestKVMDHCPPartialAndRevoked(t *testing.T) {
	api := newKVMDHCPFixtureAPI(t, 0)
	fixture := &fixtureDHCPNetwork{live: make(map[string]kvmDHCPHost), config: make(map[string]kvmDHCPHost),
		allow: true, partial: true, lostReply: true}
	if _, err := runFixtureDHCP(t, api, fixture, true); err == nil || fixture.writes != 1 {
		t.Fatal("a partial libvirt update was reported as complete")
	}
	preview, err := runFixtureDHCP(t, api, fixture, false)
	if err != nil || preview.Remaining != 3 || preview.Next[0].Live || !preview.Next[0].Config ||
		preview.Next[0].IP != fixtureIPv4Pool.First {
		t.Fatalf("retry does not isolate missing side: %+v %v", preview, err)
	}
	fixture.partial = false
	revoked := newKVMDHCPFixtureAPI(t, 2)
	if _, err := runFixtureDHCP(t, revoked, fixture, true); err == nil || fixture.writes != 2 {
		t.Fatalf("revoked authority allowed additional writes: %d %v", fixture.writes, err)
	}
	applied, err := runFixtureDHCP(t, api, fixture, true)
	if err != nil || applied.Remaining != 0 || fixture.writes != 4 {
		t.Fatalf("resume failed after authority change: %+v %v", applied, err)
	}
}

func TestKVMDHCPConflictsAndBounds(t *testing.T) {
	api := newKVMDHCPFixtureAPI(t, 0)
	for _, tc := range []struct {
		name, dynamic, lease, configUUID string
		host                             kvmDHCPHost
	}{
		{name: "dynamic", dynamic: "<range start='10.77.0.4' end='10.77.0.6'/>"},
		{name: "lease", lease: "2026-11-01 07:44:33 52:54:00:01:02:03 ipv4 10.77.0.3/29 guest -\n"},
		{name: "name", host: kvmDHCPHost{IP: "10.77.0.5", MAC: "52:54:00:01:02:03", Name: "CMP-IP-10-77-0-3"}},
		{name: "MAC", host: kvmDHCPHost{IP: "10.77.0.5", MAC: kvm.IPv4MAC("10.77.0.3"), Name: "other"}},
		{name: "replacement-network", configUUID: fixtureKVMUUID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &fixtureDHCPNetwork{live: make(map[string]kvmDHCPHost), config: make(map[string]kvmDHCPHost),
				rangeXML: tc.dynamic, leases: tc.lease, configUUID: tc.configUUID}
			if tc.host.IP != "" {
				fixture.config[tc.host.IP] = tc.host
			}
			if _, err := runFixtureDHCP(t, api, fixture, true); err == nil || fixture.writes != 0 {
				t.Fatalf("conflicting network was mutated: %d %v", fixture.writes, err)
			}
		})
	}
	plan := kvmDHCPPlan{missing: make([]kvmDHCPChange, 20)}
	for i := range plan.missing {
		plan.missing[i].Live, plan.missing[i].Config = true, true
	}
	if len(plan.batch()) != kvmDHCPBatch {
		t.Fatal("batch exceeds its address bound")
	}
}
