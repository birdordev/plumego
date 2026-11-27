package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmLiveMigrationTarget struct {
	host, vm, migration string
	generation          int64
}

type kvmLiveMigrationRunner struct {
	kvmProbe
	write    virshReader
	remote   func(context.Context, *url.URL, ...string) ([]byte, error)
	bootFile func(string, int64) error
}

type kvmLiveMigrationProfile struct {
	Destinations map[string]string `json:"destinations,omitempty"`
}

var kvmMigrationDNSLabel = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func parseKVMLiveMigrationTarget(args []string) (kvmLiveMigrationTarget, bool) {
	if len(args) != 8 || !kvmUnitName.MatchString(args[4]) || !kvmMachineUUID.MatchString(args[6]) || !kvmUnitName.MatchString(args[7]) {
		return kvmLiveMigrationTarget{}, false
	}
	generation, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil || generation < 1 {
		return kvmLiveMigrationTarget{}, false
	}
	return kvmLiveMigrationTarget{host: args[4], vm: args[6], migration: args[7], generation: generation}, true
}

func readKVMLiveProfile(input io.Reader) (map[string]*url.URL, error) {
	data, err := readOperationInput(input, 16<<10)
	if err != nil {
		return nil, err
	}
	var profile kvmLiveMigrationProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profile) != nil || decoder.Decode(new(any)) != io.EOF || len(profile.Destinations) > 64 {
		return nil, errors.New("invalid local KVM migration profile")
	}
	result := make(map[string]*url.URL, len(profile.Destinations))
	for id, raw := range profile.Destinations {
		target, err := parseKVMLiveDestination(raw)
		if err != nil || !kvmUnitName.MatchString(id) {
			return nil, errors.New("invalid local KVM migration destination")
		}
		result[id] = target
	}
	return result, nil
}

func parseKVMLiveDestination(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "qemu+tls" || target.Hostname() == "" || target.User != nil ||
		target.EscapedPath() != "/system" || target.RawQuery != "" || target.ForceQuery || strings.Contains(raw, "#") ||
		len(target.Hostname()) > 253 || !(target.Host == target.Hostname() || target.Port() != "") {
		return nil, errKVMUnsafeTarget
	}
	for _, label := range strings.Split(target.Hostname(), ".") {
		if !kvmMigrationDNSLabel.MatchString(label) {
			return nil, errKVMUnsafeTarget
		}
	}
	if port := target.Port(); port != "" {
		if value, err := strconv.ParseUint(port, 10, 16); err != nil || value == 0 {
			return nil, errKVMUnsafeTarget
		}
	}
	return target, nil
}

type kvmLiveMigrationAgent struct {
	api          kvmVMAPI
	target       *url.URL
	host         kvm.Host
	runner       kvmLiveMigrationRunner
	destinations map[string]*url.URL
}

func runKVMLiveMigration(ctx context.Context, api kvmVMAPI, target kvmLiveMigrationTarget, input io.Reader, output io.Writer, runner kvmLiveMigrationRunner) error {
	destinations, err := readKVMLiveProfile(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Generation != target.generation || !kvmMachineUUID.MatchString(host.UUID) || host.FenceState != "" {
		return errKVMUnsafeTarget
	}
	caps, err := runner.read(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(caps, host.UUID); err != nil {
		return err
	}
	agent := kvmLiveMigrationAgent{api: api, target: api.target.JoinPath("live-migrations", target.vm, target.migration),
		host: host, runner: runner, destinations: destinations}
	work, err := agent.command(ctx, kvm.LiveMigrationCommand{Action: "inspect"})
	if err != nil {
		return err
	}
	m := work.Migration
	if m.ID != target.migration || m.VM != target.vm || work.VM.ID != target.vm || (m.Source != host.ID && m.Target != host.ID) {
		return errKVMUnsafeTarget
	}
	if m.Status == "pending" || m.Status == "completed" || m.Status == "rejected" || m.Status == "aborted" {
		return json.NewEncoder(output).Encode(m)
	}
	agent.runner.shared, err = loadKVMSharedStorage(ctx, api, host)
	if err != nil {
		return err
	}
	if m.Source == host.ID {
		work, err = agent.sourceStep(ctx, work)
	} else {
		work, err = agent.targetStep(ctx, work)
	}
	if work.Migration.ID == "" {
		return err
	}
	return errors.Join(err, json.NewEncoder(output).Encode(work.Migration))
}

func (agent kvmLiveMigrationAgent) command(ctx context.Context, command kvm.LiveMigrationCommand) (kvm.LiveMigrationWork, error) {
	command.AgentIdentity = kvm.AgentIdentity{UUID: agent.host.UUID, Generation: agent.host.Generation}
	var work kvm.LiveMigrationWork
	err := agent.api.request(ctx, agent.target, http.MethodPost, command, &work)
	return work, err
}

func (agent kvmLiveMigrationAgent) targetStep(ctx context.Context, work kvm.LiveMigrationWork) (kvm.LiveMigrationWork, error) {
	m := work.Migration
	if m.Status == "dispatched" {
		return work, nil
	}
	vm := work.VM
	vm.Host, vm.Generation = agent.host.ID, agent.host.Generation
	if m.TargetGeneration != agent.host.Generation || !validKVMVMSpec(vm, agent.host) || vm.Address != "" {
		return kvm.LiveMigrationWork{}, errKVMUnsafeTarget
	}
	network, err := liveKVMNetwork(ctx, agent.runner.read, vm.Network)
	if err != nil {
		return kvm.LiveMigrationWork{}, err
	}
	if err := liveKVMVolume(ctx, agent.runner, vm); err != nil {
		return kvm.LiveMigrationWork{}, err
	}
	cmd := kvm.LiveMigrationCommand{Revision: m.Revision, NetworkSHA256: network}
	switch m.Status {
	case "approved":
		absent, err := liveKVMAbsent(ctx, agent.runner.read, vm.ID)
		if err != nil || !absent {
			return kvm.LiveMigrationWork{}, errors.Join(errKVMUnsafeTarget, err)
		}
		cmd.Action = "prepare"
	case "source_departed":
		cmd.MAC, err = liveKVMDomainMAC(ctx, agent.runner.read, vm)
		if err != nil || cmd.MAC != m.MAC || network != m.NetworkSHA256 {
			return kvm.LiveMigrationWork{}, errors.Join(errKVMUnsafeTarget, err)
		}
		cmd.Action = "complete"
	default:
		return kvm.LiveMigrationWork{}, errKVMUnsafeTarget
	}
	return agent.command(ctx, cmd)
}

func (agent kvmLiveMigrationAgent) sourceStep(ctx context.Context, work kvm.LiveMigrationWork) (kvm.LiveMigrationWork, error) {
	m, vm := work.Migration, work.VM
	if m.SourceGeneration != agent.host.Generation || !validKVMVMSpec(vm, agent.host) || vm.Address != "" {
		return kvm.LiveMigrationWork{}, errKVMUnsafeTarget
	}
	switch m.Status {
	case "source_departed":
		return work, nil
	case "dispatched":
		return agent.observeDeparture(ctx, work)
	case "approved":
		if m.PreparedAt == nil {
			return work, nil
		}
	default:
		return kvm.LiveMigrationWork{}, errKVMUnsafeTarget
	}
	destination := agent.destinations[m.Target]
	if destination == nil {
		return kvm.LiveMigrationWork{}, errors.New("KVM target is missing from the local migration profile")
	}
	mac, err := agent.checkSourceDispatch(ctx, work, destination)
	if err != nil {
		return kvm.LiveMigrationWork{}, err
	}
	armed, err := agent.command(ctx, kvm.LiveMigrationCommand{Action: "arm", Revision: m.Revision, NetworkSHA256: m.NetworkSHA256, MAC: mac})
	if err != nil {
		return kvm.LiveMigrationWork{}, err
	}
	if armed.Migration.ID != m.ID || armed.Migration.Status != "dispatched" || armed.Migration.Revision != m.Revision+1 ||
		armed.Migration.MAC != mac || armed.VM != vm || armed.TargetUUID != work.TargetUUID {
		return kvm.LiveMigrationWork{}, errKVMUnsafeTarget
	}
	if latest, err := agent.checkSourceDispatch(ctx, armed, destination); err != nil || latest != mac {
		return armed, errors.Join(errKVMUnsafeTarget, err)
	}
	current, err := agent.command(ctx, kvm.LiveMigrationCommand{Action: "inspect"})
	if err != nil || current.Migration.ID != m.ID || current.Migration.Revision != armed.Migration.Revision ||
		current.Migration.Status != "dispatched" || current.VM != vm {
		return armed, errors.Join(errKVMUnsafeTarget, err)
	}
	_, writeErr := agent.runner.write(ctx, "migrate", "--live", "--p2p", "--tunnelled", "--persistent",
		"--undefinesource", "--change-protection", "--abort-on-error", vm.ID, destination.String())
	observed, observeErr := agent.observeDeparture(ctx, armed)
	return observed, errors.Join(writeErr, observeErr)
}

func (agent kvmLiveMigrationAgent) checkSourceDispatch(ctx context.Context, work kvm.LiveMigrationWork, destination *url.URL) (string, error) {
	if err := liveKVMVolume(ctx, agent.runner, work.VM); err != nil {
		return "", err
	}
	local, err := liveKVMNetwork(ctx, agent.runner.read, work.VM.Network)
	if err != nil || local != work.Migration.NetworkSHA256 {
		return "", errors.Join(errKVMNetworkMapping, err)
	}
	remote := func(ctx context.Context, args ...string) ([]byte, error) {
		return agent.runner.remote(ctx, destination, args...)
	}
	caps, err := remote(ctx, "capabilities")
	if err != nil {
		return "", err
	}
	if _, err := readKVMMemory(caps, work.TargetUUID); err != nil {
		return "", err
	}
	targetNetwork, err := liveKVMNetwork(ctx, remote, work.VM.Network)
	if err != nil || targetNetwork != local {
		return "", errors.Join(errKVMNetworkMapping, err)
	}
	absent, err := liveKVMAbsent(ctx, remote, work.VM.ID)
	if err != nil || !absent {
		return "", errors.Join(errKVMUnsafeTarget, err)
	}
	return liveKVMDomainMAC(ctx, agent.runner.read, work.VM)
}

func (agent kvmLiveMigrationAgent) observeDeparture(ctx context.Context, work kvm.LiveMigrationWork) (kvm.LiveMigrationWork, error) {
	absent, err := liveKVMAbsent(ctx, agent.runner.read, work.VM.ID)
	if err != nil || !absent {
		return work, err
	}
	return agent.command(ctx, kvm.LiveMigrationCommand{Action: "depart", Revision: work.Migration.Revision})
}
