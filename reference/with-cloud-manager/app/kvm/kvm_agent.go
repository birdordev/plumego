package kvmapp

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

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/internal/kvm"
)

type VirshReader func(context.Context, ...string) ([]byte, error)

func ObserveKVMHost(ctx context.Context, target *url.URL, hostID, generation, token string, input io.Reader, output io.Writer, transport http.RoundTripper, probe KVMProbe) error {
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
	readPlan := projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}
	if err := projecthttp.Run(ctx, readPlan, token, nil, &response, transport); err != nil {
		return err
	}
	var host kvm.Host
	if json.Unmarshal(response.Bytes(), &host) != nil || host.ID != hostID ||
		host.Generation != expected || host.Revision < 1 || host.Sequence < 0 || host.Sequence == math.MaxInt64 {
		return errors.New("KVM host or agent generation does not match")
	}
	if host.FenceState != "" {
		return kvm.ErrConflict
	}
	api := KVMVMAPI{Target: target, Token: token, Transport: transport}
	probe.shared, err = loadKVMSharedStorage(ctx, api, host)
	if err != nil {
		return err
	}
	sampling, stopSampling := context.WithTimeout(ctx, 12*time.Second)
	defer stopSampling()
	capacity, sampleErr := probe.capacity(sampling, host, limits)
	if sampleErr != nil && probe.shared.ID == "" {
		return sampleErr
	}
	domains := []string{}
	var evidence *kvm.SharedStorageEvidence
	if capacity.VCPU > 0 && capacity.MemoryMiB > 0 && capacity.DiskMiB > 0 {
		verified, ok := probe.CapacityDomains(sampling, host, api.lookupInventory)
		if ok {
			domains = verified
			if probe.shared.ID != "" {
				evidence = &kvm.SharedStorageEvidence{ID: probe.shared.ID, SharedStorageSource: probe.shared.SharedStorageSource}
			}
		} else {
			capacity = kvm.Capacity{}
		}
	}
	if probe.shared.ID != "" && evidence == nil {
		capacity = kvm.Capacity{}
	}
	body, err := json.Marshal(kvm.Report{UUID: host.UUID, Generation: expected, Sequence: host.Sequence + 1, Capacity: capacity,
		Inventory: &kvm.CapacityInventory{HostRevision: host.Revision, Domains: domains}, SharedStorage: evidence})
	if err != nil {
		return err
	}
	writePlan := projecthttp.Request{Target: target.JoinPath("capacity"), Method: http.MethodPut, Status: http.StatusOK, Body: body}
	return errors.Join(sampleErr, projecthttp.Run(ctx, writePlan, token, nil, output, transport))
}

func readKVMHostLimits(input io.Reader) (kvm.Capacity, error) {
	data, err := projecthttp.ReadInput(input, 1024)
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
	return invokeKVMVirsh(ctx, "qemu:///system", args...)
}

func invokeKVMVirsh(ctx context.Context, connection string, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("local KVM capacity collection requires Linux")
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/virsh", append([]string{"--connect", connection}, args...)...)
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
