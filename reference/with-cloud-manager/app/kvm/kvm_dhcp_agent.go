package kvmapp

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"with-cloud-manager/internal/kvm"
)

type KVMDHCPTarget struct {
	Host       string
	Generation string
	Network    string
	Uuid       string
	Apply      bool
}

type KVMDHCPReport struct {
	Network         string          `json:"network"`
	NetworkUUID     string          `json:"network_uuid"`
	Addresses       int             `json:"addresses"`
	Remaining       int             `json:"remaining"`
	VerifiedUpdates int             `json:"verified_updates"`
	RecoveredReply  bool            `json:"recovered_reply,omitempty"`
	Next            []KVMDHCPChange `json:"next"`
}

func RunKVMDHCP(ctx context.Context, api KVMVMAPI, target KVMDHCPTarget, output io.Writer, read, write VirshReader) error {
	generation, err := strconv.ParseInt(target.Generation, 10, 64)
	if err != nil || generation < 1 || !kvmUnitName.MatchString(target.Network) || !kvmMachineUUID.MatchString(target.Uuid) {
		return errKVMNetworkMapping
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.Target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.Host || host.Generation != generation || host.FenceState != "" || !kvmMachineUUID.MatchString(host.UUID) {
		return errKVMNetworkMapping
	}
	caps, err := read(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(caps, host.UUID); err != nil {
		return err
	}
	agent := kvm.AgentIdentity{UUID: host.UUID, Generation: generation}
	loadPool := func() (kvm.IPv4Pool, error) {
		var pool kvm.IPv4Pool
		err := api.request(ctx, api.Target.JoinPath("dhcp-plan", target.Network), http.MethodPost, agent, &pool)
		return pool, err
	}
	pool, err := loadPool()
	if err != nil {
		return err
	}
	if pool.Site != host.Site || pool.Network != target.Network || !pool.Valid() {
		return errKVMNetworkMapping
	}
	desired := newKVMDHCPDesired(pool.IPv4PoolSpec)
	plan, err := desired.observe(ctx, read, target.Uuid)
	if err != nil {
		return err
	}
	report := KVMDHCPReport{Network: pool.Network, NetworkUUID: target.Uuid, Addresses: len(desired.hosts)}
	if target.Apply && len(plan.Missing) > 0 {
		authorize := func() error {
			current, err := loadPool()
			if err != nil {
				return err
			}
			if current.Site != pool.Site || current.IPv4PoolSpec != pool.IPv4PoolSpec {
				return errKVMNetworkMapping
			}
			return nil
		}
		// 每次出站调用都查验当前代理身份。外部效果由随后的只读观测确认。
		attempted, writeErr, err := plan.apply(ctx, target.Uuid, authorize, write)
		if err != nil {
			return err
		}
		after, err := desired.observe(ctx, read, target.Uuid)
		if err != nil {
			return errors.Join(writeErr, err)
		}
		plan = after
		report.VerifiedUpdates, err = plan.verify(attempted)
		if err != nil {
			return errors.Join(writeErr, err)
		}
		report.RecoveredReply = writeErr != nil
	}
	report.Remaining = len(plan.Missing)
	report.Next = append([]KVMDHCPChange{}, plan.Missing[:min(KVMDHCPBatch, report.Remaining)]...)
	return json.NewEncoder(output).Encode(report)
}

type kvmDHCPCommand struct {
	KVMDHCPChange
	index int
}

func (plan KVMDHCPPlan) Batch() []kvmDHCPCommand {
	var commands []kvmDHCPCommand
	for _, entry := range plan.Missing[:min(KVMDHCPBatch, len(plan.Missing))] {
		if entry.Live && entry.Config && plan.liveIndex != plan.configIndex {
			commands = append(commands,
				kvmDHCPCommand{KVMDHCPChange: KVMDHCPChange{KVMDHCPHost: entry.KVMDHCPHost, Live: true}, index: plan.liveIndex},
				kvmDHCPCommand{KVMDHCPChange: KVMDHCPChange{KVMDHCPHost: entry.KVMDHCPHost, Config: true}, index: plan.configIndex})
			continue
		}
		index := plan.configIndex
		if entry.Live {
			index = plan.liveIndex
		}
		commands = append(commands, kvmDHCPCommand{KVMDHCPChange: entry, index: index})
	}
	return commands
}

func (plan KVMDHCPPlan) apply(ctx context.Context, uuid string, authorize func() error, write VirshReader) ([]KVMDHCPChange, error, error) {
	var attempted []KVMDHCPChange
	for _, command := range plan.Batch() {
		document, err := xml.Marshal(command.KVMDHCPHost)
		if err != nil {
			return nil, nil, err
		}
		args := []string{"net-update", uuid, "add-last", "ip-dhcp-host", string(document), "--parent-index", strconv.Itoa(command.index)}
		if command.Live {
			args = append(args, "--live")
		}
		if command.Config {
			args = append(args, "--config")
		}
		if err := authorize(); err != nil {
			return nil, nil, err
		}
		attempted = append(attempted, command.KVMDHCPChange)
		if _, err := write(ctx, args...); err != nil {
			return attempted, err, nil
		}
	}
	return attempted, nil, nil
}

func (plan KVMDHCPPlan) verify(attempted []KVMDHCPChange) (int, error) {
	missing := make(map[string]KVMDHCPChange, len(plan.Missing))
	for _, entry := range plan.Missing {
		missing[entry.IP] = entry
	}
	verified := 0
	for _, entry := range attempted {
		pending := missing[entry.IP]
		if (entry.Live && pending.Live) || (entry.Config && pending.Config) {
			return verified, errKVMNetworkMapping
		}
		if entry.Live {
			verified++
		}
		if entry.Config {
			verified++
		}
	}
	return verified, nil
}
