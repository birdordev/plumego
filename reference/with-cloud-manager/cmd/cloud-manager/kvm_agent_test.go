package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func fixtureKVMProbe(t *testing.T, capabilitiesUUID string, domains string, storageMiB int64) kvmProbe {
	t.Helper()
	reader := func(_ context.Context, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		responses := map[string]string{
			"capabilities": `<capabilities><host><uuid>` + capabilitiesUUID + `</uuid><topology><cells>
			<cell id="0"><memory unit="KiB">4194304</memory></cell>
			<cell id="1"><memory unit="KiB">4194304</memory></cell>
			</cells></topology></host><guest><arch><domain type="kvm"/></arch></guest></capabilities>`,
			"list --all --uuid": domains,
			"net-list --name":   "app\nbackup\n",
			"nodecpumap":        "CPUs present:   16\nCPUs online:    8\nCPU map:        yyyyyyyynnnnnnnn\n",
			"pool-list --name":  "local\n",
			"pool-dumpxml --pool local": `<pool type="dir"><name>local</name>
			<target><path>/srv/kvm</path></target></pool>`,
		}
		data, ok := responses[command]
		if !ok {
			t.Errorf("unexpected local libvirt read %q", command)
		}
		return []byte(data), nil
	}
	return kvmProbe{read: reader, freeMiB: func(path string) (int64, error) {
		if path != "/srv/kvm" {
			t.Errorf("unexpected pool path %q", path)
		}
		return storageMiB, nil
	}}
}

func TestKVMObserveHostAgent(t *testing.T) {
	host := kvm.Host{HostSpec: fixtureKVMHost(), Site: "site-a", Revision: 2, Generation: 3, Sequence: 9}
	want := kvm.Report{UUID: host.UUID, Generation: 3, Sequence: 10,
		Capacity:  kvm.Capacity{VCPU: 6, MemoryMiB: 8192, DiskMiB: 4096},
		Inventory: &kvm.CapacityInventory{HostRevision: host.Revision, Domains: []string{}}}
	for _, test := range []struct {
		name, uuid, domains, generation string
		writes                          int
		unavailable                     bool
	}{
		{"sample", host.UUID, "", "3", 1, false},
		{"occupied", host.UUID, "b0113615-a49b-426a-9160-5742d69004ef\n", "3", 1, true},
		{"wrong-machine", "another-machine", "", "3", 0, false},
		{"old-generation", host.UUID, "", "2", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mutex sync.Mutex
			var requests []string
			var posted kvm.Report
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+fixtureKVMAgent {
					t.Error("agent did not send its own credential")
				}
				mutex.Lock()
				defer mutex.Unlock()
				w.Header().Set("Content-Type", "application/json")
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					writeCloudFixtureJSON(t, w, map[string]any{"data": host})
				case http.MethodPost:
					var query kvm.VMInventoryQuery
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil || query.UUID != host.UUID ||
						query.Generation != host.Generation || !slices.Equal(query.Domains, strings.Fields(test.domains)) {
						t.Errorf("incorrect inventory lookup: %+v %v", query, err)
					}
					writeCloudFixtureJSON(t, w, map[string]any{"data": kvm.VMInventory{VMs: []kvm.VM{}}})
				case http.MethodPut:
					if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
						t.Error(err)
					}
					writeCloudFixtureJSON(t, w, map[string]any{"data": host})
				default:
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(server.Close)
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			target := base.JoinPath(fixtureKVMSite, "hosts", host.ID)
			var output bytes.Buffer
			err = observeKVMHost(t.Context(), target, host.ID, test.generation, fixtureKVMAgent,
				strings.NewReader(`{"vcpu":6,"memory_mib":12288,"disk_mib":8192}`), &output,
				server.Client().Transport, fixtureKVMProbe(t, test.uuid, test.domains, 4096))
			mutex.Lock()
			defer mutex.Unlock()
			wantRequests := []string{"GET " + fixtureKVMSite + "/hosts/" + host.ID}
			if test.unavailable {
				wantRequests = append(wantRequests, "POST "+fixtureKVMSite+"/hosts/"+host.ID+"/vm-inventory")
			}
			if test.writes > 0 {
				wantRequests = append(wantRequests, "PUT "+fixtureKVMSite+"/hosts/"+host.ID+"/capacity")
			}
			if (err != nil) != (test.writes == 0) || !slices.Equal(requests, wantRequests) {
				t.Fatalf("unexpected agent request count or result: %v, %v", requests, err)
			}
			if test.writes == 0 {
				if test.name == "wrong-machine" && !errors.Is(err, errInvalidKVMObservation) {
					t.Fatalf("host mismatch produced unexpected error: %v", err)
				}
				if test.name == "old-generation" && !strings.Contains(err.Error(), "generation does not match") {
					t.Fatalf("agent generation failure was not reported: %v", err)
				}
				return
			}
			expected := want
			if test.unavailable {
				expected.Capacity = kvm.Capacity{}
			}
			if !reflect.DeepEqual(posted, expected) {
				t.Fatalf("incorrect report or endpoint: %+v %v", posted, requests)
			}
		})
	}
}
