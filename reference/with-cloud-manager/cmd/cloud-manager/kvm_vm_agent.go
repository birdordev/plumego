package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmVMRunner struct {
	kvmProbe
	write      virshReader
	bootFile   func(string, int64) error
	enableACPI bool
}

type kvmVMAPI struct {
	target    *url.URL
	token     string
	transport http.RoundTripper
}

func (a kvmVMAPI) request(ctx context.Context, target *url.URL, method string, input, response any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	var payload bytes.Buffer
	plan := operationRequestPlan{target: target, method: method, status: http.StatusOK, body: body}
	if err := runProjectRequest(ctx, plan, a.token, nil, &payload, a.transport); err != nil {
		return err
	}
	if err := json.Unmarshal(payload.Bytes(), response); err != nil {
		return errors.New("invalid KVM VM API response")
	}
	return nil
}

func runKVMVMJob(ctx context.Context, api kvmVMAPI, hostID, generation string, input io.Reader, output io.Writer, runner kvmVMRunner) error {
	profile, err := readKVMAgentProfile(input)
	if err != nil {
		return err
	}
	expected, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || expected < 1 {
		return errors.New("invalid expected KVM agent generation")
	}
	duration := 75 * time.Second
	if profile.ImageRoot != "" {
		duration = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != hostID || host.Generation != expected || !kvmMachineUUID.MatchString(host.UUID) {
		return errors.New("KVM host or agent generation does not match")
	}
	caps, err := runner.read(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(caps, host.UUID); err != nil {
		return err
	}
	agent := kvm.AgentIdentity{UUID: host.UUID, Generation: expected}
	poll := api.target.JoinPath("vm-work")
	var work kvm.VMWork
	if err := api.request(ctx, poll, http.MethodPost, agent, &work); err != nil {
		return err
	}
	if work.Job == nil {
		return json.NewEncoder(output).Encode(work)
	}
	job := *work.Job
	if !validKVMJob(job, host) {
		return errKVMUnsafeTarget
	}
	jobTarget := poll.JoinPath(job.ID)
	if job.State == "staging" || (job.State == "queued" && job.ImageID != "") {
		return runKVMImageStage(ctx, api, jobTarget, agent, job, profile, output, runner)
	}
	switch job.State {
	case "queued", "staged", "defined":
		if job.ImageID == "" && !slices.Contains(profile.Volumes, job.Volume) {
			return errKVMUnsafeTarget
		}
		if err := validateKVMVolume(ctx, runner, job); err != nil {
			return err
		}
		if job.ImageID != "" {
			if profile.ImageRoot == "" {
				return errKVMUnsafeTarget
			}
			path, err := runner.poolPath(ctx, job.StoragePool)
			if err != nil {
				return err
			}
			if err := verifyKVMImageVolume(ctx, path, job); err != nil {
				return err
			}
		}
		next, local := "defining", "absent"
		if job.State == "defined" {
			next, local = "starting", "defined"
		}
		if err := checkKVMBeforeWrite(ctx, runner.read, job, local); err != nil {
			return err
		}
		job, err := armKVMJob(ctx, api, jobTarget, agent, job, next)
		if err != nil {
			return err
		}
		if err := validateKVMVolume(ctx, runner, job); err != nil {
			return err
		}
		if err := checkKVMBeforeWrite(ctx, runner.read, job, local); err != nil {
			return err
		}
		// 写入结果未知时仍尝试点查。后续轮询只观测已登记的步骤。
		writeErr := runner.mutate(ctx, job)
		return errors.Join(observeKVMJob(ctx, api, jobTarget, agent, job, output, runner.read), writeErr)
	case "defining", "starting":
		return observeKVMJob(ctx, api, jobTarget, agent, job, output, runner.read)
	case "shutdown_queued", "stopping":
		return runKVMShutdownJob(ctx, api, jobTarget, agent, job, output, runner)
	case "retire_queued", "undefining":
		return runKVMRetirementJob(ctx, api, jobTarget, agent, job, output, runner)
	default:
		return errKVMUnsafeTarget
	}
}

func validKVMJob(job kvm.VM, host kvm.Host) bool {
	switch job.State {
	case "shutdown_queued", "stopping", "retire_queued", "undefining":
		return job.Generation > 0 && job.Generation <= host.Generation && validKVMVMSpec(job, host)
	default:
		return job.Generation == host.Generation && validKVMVMSpec(job, host)
	}
}

func armKVMJob(ctx context.Context, api kvmVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, next string) (kvm.VM, error) {
	command := kvm.VMCommand{AgentIdentity: agent, Revision: job.Revision}
	var armed kvm.VM
	if err := api.request(ctx, target.JoinPath("arm"), http.MethodPost, command, &armed); err != nil {
		return kvm.VM{}, err
	}
	job.State, job.Revision, job.Observation = next, job.Revision+1, ""
	if armed != job {
		return kvm.VM{}, errKVMUnsafeTarget
	}
	return armed, nil
}

func validKVMVMSpec(job kvm.VM, host kvm.Host) bool {
	return job.Host == host.ID && kvmMachineUUID.MatchString(job.ID) && validKVMNetworkAddress(job) && validKVMImageSpec(job) &&
		kvmSafePlacement.MatchString(job.Placement) && kvmUnitName.MatchString(job.Volume) &&
		kvmUnitName.MatchString(job.StoragePool) && kvmUnitName.MatchString(job.Network) &&
		job.StoragePool == host.StoragePool && slices.Contains(host.Networks, job.Network) && job.Revision > 0 &&
		job.Capacity.GPU == 0 && job.Capacity.VCPU > 0 && job.Capacity.VCPU <= 4096 &&
		job.Capacity.MemoryMiB > 0 && job.Capacity.MemoryMiB <= 1<<24 && job.Capacity.DiskMiB > 0 && job.Capacity.DiskMiB <= 1<<40
}

func checkKVMBeforeWrite(ctx context.Context, read virshReader, job kvm.VM, expected string) error {
	observed, err := inspectLocalVM(ctx, read, job)
	if err != nil {
		return err
	}
	if observed.state != expected {
		return errKVMUnsafeTarget
	}
	return nil
}

func observeKVMJob(ctx context.Context, api kvmVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, output io.Writer, read virshReader) error {
	observed, err := inspectLocalVM(ctx, read, job)
	if err != nil {
		return err
	}
	if job.State == "starting" && observed.state == "running" {
		if err := validateKVMNetwork(ctx, read, job); err != nil {
			return err
		}
	}
	return submitKVMObservation(ctx, api, target, agent, job, observed.state, output)
}

func submitKVMObservation(ctx context.Context, api kvmVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, state string, output io.Writer) error {
	var updated kvm.VM
	sample := kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: job.Revision}, Observed: state}
	if err := api.request(ctx, target.JoinPath("observation"), http.MethodPut, sample, &updated); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(updated)
}

func (runner kvmVMRunner) mutate(ctx context.Context, job kvm.VM) error {
	if job.State == "starting" {
		_, err := runner.write(ctx, "start", job.ID)
		return err
	}
	if job.State != "defining" {
		return errKVMUnsafeTarget
	}
	file, err := os.CreateTemp("", "cloud-manager-kvm-*.xml")
	if err != nil {
		return errors.New("cannot create local libvirt specification")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	domain := desiredKVMDomain(job)
	if runner.enableACPI {
		domain.Features = &kvmFeatures{ACPI: &struct{}{}}
	}
	encodeErr := xml.NewEncoder(file).Encode(domain)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		return errors.New("cannot write local libvirt specification")
	}
	_, err = runner.write(ctx, "define", file.Name(), "--validate")
	return err
}
