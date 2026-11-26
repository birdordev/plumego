package main

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

type kvmDHCPTarget struct {
	host       string
	generation string
	network    string
	uuid       string
	apply      bool
}

type kvmDHCPReport struct {
	Network         string          `json:"network"`
	NetworkUUID     string          `json:"network_uuid"`
	Addresses       int             `json:"addresses"`
	Remaining       int             `json:"remaining"`
	VerifiedUpdates int             `json:"verified_updates"`
	RecoveredReply  bool            `json:"recovered_reply,omitempty"`
	Next            []kvmDHCPChange `json:"next"`
}

func runKVMDHCP(ctx context.Context, api kvmVMAPI, target kvmDHCPTarget, output io.Writer, read, write virshReader) error {
	generation, err := strconv.ParseInt(target.generation, 10, 64)
	if err != nil || generation < 1 || !kvmUnitName.MatchString(target.network) || !kvmMachineUUID.MatchString(target.uuid) {
		return errKVMNetworkMapping
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Generation != generation || !kvmMachineUUID.MatchString(host.UUID) {
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
		err := api.request(ctx, api.target.JoinPath("dhcp-plan", target.network), http.MethodPost, agent, &pool)
		return pool, err
	}
	pool, err := loadPool()
	if err != nil {
		return err
	}
	if pool.Site != host.Site || pool.Network != target.network || !pool.IPv4PoolSpec.Valid() {
		return errKVMNetworkMapping
	}
	desired := newKVMDHCPDesired(pool.IPv4PoolSpec)
	plan, err := desired.observe(ctx, read, target.uuid)
	if err != nil {
		return err
	}
	report := kvmDHCPReport{Network: pool.Network, NetworkUUID: target.uuid, Addresses: len(desired.hosts)}
	if target.apply && len(plan.missing) > 0 {
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
		attempted, writeErr, err := plan.apply(ctx, target.uuid, authorize, write)
		if err != nil {
			return err
		}
		after, err := desired.observe(ctx, read, target.uuid)
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
	report.Remaining = len(plan.missing)
	report.Next = append([]kvmDHCPChange{}, plan.missing[:min(kvmDHCPBatch, report.Remaining)]...)
	return json.NewEncoder(output).Encode(report)
}

type kvmDHCPCommand struct {
	kvmDHCPChange
	index int
}

func (plan kvmDHCPPlan) batch() []kvmDHCPCommand {
	var commands []kvmDHCPCommand
	for _, entry := range plan.missing[:min(kvmDHCPBatch, len(plan.missing))] {
		if entry.Live && entry.Config && plan.liveIndex != plan.configIndex {
			commands = append(commands,
				kvmDHCPCommand{kvmDHCPChange: kvmDHCPChange{kvmDHCPHost: entry.kvmDHCPHost, Live: true}, index: plan.liveIndex},
				kvmDHCPCommand{kvmDHCPChange: kvmDHCPChange{kvmDHCPHost: entry.kvmDHCPHost, Config: true}, index: plan.configIndex})
			continue
		}
		index := plan.configIndex
		if entry.Live {
			index = plan.liveIndex
		}
		commands = append(commands, kvmDHCPCommand{kvmDHCPChange: entry, index: index})
	}
	return commands
}

func (plan kvmDHCPPlan) apply(ctx context.Context, uuid string, authorize func() error, write virshReader) ([]kvmDHCPChange, error, error) {
	var attempted []kvmDHCPChange
	for _, command := range plan.batch() {
		document, err := xml.Marshal(command.kvmDHCPHost)
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
		attempted = append(attempted, command.kvmDHCPChange)
		if _, err := write(ctx, args...); err != nil {
			return attempted, err, nil
		}
	}
	return attempted, nil, nil
}

func (plan kvmDHCPPlan) verify(attempted []kvmDHCPChange) (int, error) {
	missing := make(map[string]kvmDHCPChange, len(plan.missing))
	for _, entry := range plan.missing {
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
