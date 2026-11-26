package main

import (
	"context"
	"net/http"
	"slices"
	"sync"

	"with-cloud-manager/internal/kvm"
)

type kvmInventoryReader func(context.Context, kvm.Host, []string) ([]kvm.VM, error)

func (api kvmVMAPI) lookupInventory(ctx context.Context, host kvm.Host, ids []string) ([]kvm.VM, error) {
	var inventory kvm.VMInventory
	query := kvm.VMInventoryQuery{AgentIdentity: kvm.AgentIdentity{UUID: host.UUID, Generation: host.Generation}, Domains: ids}
	err := api.request(ctx, api.target.JoinPath("vm-inventory"), http.MethodPost, query, &inventory)
	return inventory.VMs, err
}

func (probe kvmProbe) capacityDomains(ctx context.Context, host kvm.Host, lookup kvmInventoryReader) ([]string, bool) {
	domains, err := localKVMUUIDs(ctx, probe.read)
	if err != nil || len(domains) > kvm.MaxObservedDomains {
		return nil, false
	}
	slices.Sort(domains)
	if len(domains) > 0 {
		records, err := lookup(ctx, host, domains)
		if err != nil || !inventoryMatchesDomains(host, records, domains) ||
			!probe.verifyResidentDomains(ctx, records) {
			return nil, false
		}
		persistent, err := localKVMUUIDs(ctx, probe.read, "--persistent")
		if err != nil {
			return nil, false
		}
		slices.Sort(persistent)
		if !slices.Equal(domains, persistent) {
			return nil, false
		}
	}

	// 只在同一次采样前后的本地域集合一致时上报非零容量。
	final, err := localKVMUUIDs(ctx, probe.read)
	if err != nil || ctx.Err() != nil {
		return nil, false
	}
	slices.Sort(final)
	return domains, slices.Equal(domains, final)
}

func inventoryMatchesDomains(host kvm.Host, records []kvm.VM, domains []string) bool {
	if len(records) != len(domains) {
		return false
	}
	remaining := make(map[string]struct{}, len(domains))
	for _, id := range domains {
		remaining[id] = struct{}{}
	}
	for _, vm := range records {
		if _, ok := remaining[vm.ID]; !ok || !validKVMVMSpec(vm, host) || vm.Generation < 1 || vm.Generation > host.Generation {
			return false
		}
		switch vm.State {
		case "defining", "defined", "starting", "running", "shutdown_requested", "shutdown_queued", "stopping", "stopped",
			"retire_requested", "retire_queued", "undefining":
			delete(remaining, vm.ID)
		default:
			return false
		}
	}
	return true
}

func (probe kvmProbe) verifyResidentDomains(ctx context.Context, records []kvm.VM) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	workers := min(4, len(records))
	var wg sync.WaitGroup
	for start := range workers {
		wg.Go(func() {
			for i := start; i < len(records) && ctx.Err() == nil; i += workers {
				vm := records[i]
				observed, err := inspectPresentKVMVM(ctx, probe.read, vm)
				if err != nil || !matchesResidentState(vm.State, observed.state) {
					cancel()
					return
				}
			}
		})
	}
	wg.Wait()
	return ctx.Err() == nil
}

func matchesResidentState(recorded, observed string) bool {
	if observed == "defined" {
		return true
	}
	if observed != "running" {
		return false
	}
	switch recorded {
	case "starting", "running", "shutdown_requested", "shutdown_queued", "stopping":
		return true
	default:
		return false
	}
}
