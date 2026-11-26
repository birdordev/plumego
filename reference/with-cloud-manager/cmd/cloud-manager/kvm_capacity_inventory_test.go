package main

import (
	"context"
	"encoding/xml"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestKVMCapacityInventorySnapshot(t *testing.T) {
	firstID := "b0113615-a49b-426a-9160-5742d69004ef"
	secondID := "f2ed3615-a49b-426a-9160-5742d69004ef"
	host := kvm.Host{HostSpec: fixtureKVMHost(), Generation: 2}
	first := kvm.VM{ID: firstID, Placement: "plc-" + strings.Repeat("a", 32), Host: host.ID,
		Generation: 1, Network: "app", StoragePool: "local", Volume: "disk-a", State: "running", Revision: 4,
		Capacity: kvm.Capacity{VCPU: 1, MemoryMiB: 512, DiskMiB: 1024}}
	second := first
	second.ID, second.Volume, second.Generation, second.State = secondID, "disk-b", 2, "defined"
	documents := make(map[string][]byte)
	for _, vm := range []kvm.VM{first, second} {
		data, err := xml.Marshal(desiredKVMDomain(vm))
		if err != nil {
			t.Fatal(err)
		}
		documents[vm.ID] = data
	}

	for _, tc := range []struct {
		name, final, state string
		cancel             bool
		transient          bool
		ok                 bool
	}{
		{name: "same-set-reordered", final: firstID + "\n" + secondID, state: "shut off", ok: true},
		{name: "missing-after-read", final: firstID, state: "shut off"},
		{name: "duplicate-after-read", final: firstID + "\n" + firstID, state: "shut off"},
		{name: "start-without-grant", final: firstID + "\n" + secondID, state: "running"},
		{name: "transient-domain", final: firstID + "\n" + secondID, state: "shut off", transient: true},
		{name: "canceled", final: firstID + "\n" + secondID, state: "shut off", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			listed := 0
			probe := kvmProbe{read: func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "list":
					if slices.Contains(args, "--persistent") {
						if tc.transient {
							return []byte(secondID), nil
						}
						return []byte(firstID + "\n" + secondID), nil
					}
					listed++
					if listed == 1 {
						return []byte(secondID + "\n" + firstID), nil
					}
					return []byte(tc.final), nil
				case "dumpxml":
					return documents[args[1]], ctx.Err()
				case "domstate":
					if args[1] == firstID {
						return []byte("running"), ctx.Err()
					}
					return []byte(tc.state), ctx.Err()
				default:
					t.Errorf("unexpected libvirt command: %v", args)
					return nil, errKVMUnsafeTarget
				}
			}}
			lookups := 0
			domains, ok := probe.capacityDomains(ctx, host, func(ctx context.Context, _ kvm.Host, ids []string) ([]kvm.VM, error) {
				lookups++
				if !slices.Equal(ids, []string{firstID, secondID}) {
					t.Errorf("unexpected lookup: %v", ids)
				}
				if tc.cancel {
					cancel()
				}
				return []kvm.VM{second, first}, ctx.Err()
			})
			if lookups != 1 || ok != tc.ok || (ok && !slices.Equal(domains, []string{firstID, secondID})) {
				t.Fatalf("unexpected sample: domains=%v verified=%t lookups=%d", domains, ok, lookups)
			}
		})
	}
}
