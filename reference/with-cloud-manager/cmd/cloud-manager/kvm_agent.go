package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"with-cloud-manager/internal/kvm"
)

type virshReader func(context.Context, ...string) ([]byte, error)

func observeKVMHost(ctx context.Context, target *url.URL, hostID, generation, token string, input io.Reader, output io.Writer, transport http.RoundTripper, probe kvmProbe) error {
	limits, err := readKVMHostLimits(input)
	if err != nil {
		return err
	}
	expected, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || expected < 1 {
		return errors.New("invalid expected KVM agent generation")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	var response bytes.Buffer
	readPlan := operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}
	if err := runProjectRequest(ctx, readPlan, token, nil, &response, transport); err != nil {
		return err
	}
	var host kvm.Host
	if json.Unmarshal(response.Bytes(), &host) != nil || host.ID != hostID ||
		host.Generation != expected || host.Revision < 1 || host.Sequence < 0 || host.Sequence == math.MaxInt64 {
		return errors.New("KVM host or agent generation does not match")
	}
	sampling, stopSampling := context.WithTimeout(ctx, 12*time.Second)
	defer stopSampling()
	capacity, err := probe.capacity(sampling, host, limits)
	if err != nil {
		return err
	}
	domains := []string{}
	if capacity.VCPU > 0 && capacity.MemoryMiB > 0 && capacity.DiskMiB > 0 {
		api := kvmVMAPI{target: target, token: token, transport: transport}
		verified, ok := probe.capacityDomains(sampling, host, api.lookupInventory)
		if ok {
			domains = verified
		} else {
			capacity = kvm.Capacity{}
		}
	}
	body, err := json.Marshal(kvm.Report{UUID: host.UUID, Generation: expected, Sequence: host.Sequence + 1, Capacity: capacity,
		Inventory: &kvm.CapacityInventory{HostRevision: host.Revision, Domains: domains}})
	if err != nil {
		return err
	}
	writePlan := operationRequestPlan{target: target.JoinPath("capacity"), method: http.MethodPut, status: http.StatusOK, body: body}
	return runProjectRequest(ctx, writePlan, token, nil, output, transport)
}

func readKVMHostLimits(input io.Reader) (kvm.Capacity, error) {
	data, err := readOperationInput(input, 1024)
	if err != nil {
		return kvm.Capacity{}, err
	}
	var value kvm.Capacity
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || value.VCPU < 1 || value.VCPU > 4096 ||
		value.MemoryMiB < 1 || value.MemoryMiB > 1<<24 || value.DiskMiB < 1 || value.DiskMiB > 1<<40 || value.GPU != 0 {
		return kvm.Capacity{}, errors.New("invalid local KVM allocation limits")
	}
	return value, nil
}

func runLocalVirsh(ctx context.Context, args ...string) ([]byte, error) {
	return invokeLocalVirsh(ctx, append([]string{"--readonly"}, args...)...)
}

func invokeLocalVirsh(ctx context.Context, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("local KVM capacity collection requires Linux")
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/virsh", append([]string{"--connect", "qemu:///system"}, args...)...)
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin"}
	cmd.WaitDelay = time.Second
	var output cappedVirshOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("local libvirt read failed")
	}
	return output.Bytes(), nil
}

type cappedVirshOutput struct{ bytes.Buffer }

func (out *cappedVirshOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-out.Len() {
		return 0, errors.New("local libvirt response exceeds size limit")
	}
	return out.Buffer.Write(p)
}
