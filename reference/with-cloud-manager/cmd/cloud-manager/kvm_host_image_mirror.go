package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmHostImageTarget struct {
	site, host, image string
	generation        int64
}

func parseKVMHostImageTarget(args []string) (kvmHostImageTarget, bool) {
	if len(args) != 7 || !kvmUnitName.MatchString(args[4]) || !kvmUnitName.MatchString(args[6]) {
		return kvmHostImageTarget{}, false
	}
	generation, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil || generation < 1 {
		return kvmHostImageTarget{}, false
	}
	return kvmHostImageTarget{site: args[3], host: args[4], image: args[6], generation: generation}, true
}

func runKVMHostImageMirror(ctx context.Context, api kvmVMAPI, target kvmHostImageTarget, input io.Reader, output io.Writer, probe virshReader) error {
	mirror, err := readKVMImageMirror(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Site != target.site || host.Generation != target.generation || host.FenceState != "" ||
		!kvmMachineUUID.MatchString(host.UUID) {
		return errKVMUnsafeTarget
	}
	capabilities, err := probe(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(capabilities, host.UUID); err != nil {
		return err
	}
	agent := kvm.AgentIdentity{UUID: host.UUID, Generation: host.Generation}
	imageURL := api.target.JoinPath("images", target.image)
	checkURL := imageURL.JoinPath("check")
	var current kvm.HostImageReceipt
	if err := api.request(ctx, checkURL, http.MethodPost, agent, &current); err != nil {
		return err
	}
	if !current.Valid() || current.ID != target.image || current.Host != host.ID || current.Generation != host.Generation || current.Revision < 0 {
		return errors.New("KVM host image check did not match the requested image")
	}
	authorize := func() error {
		var latest kvm.HostImageReceipt
		if err := api.request(ctx, checkURL, http.MethodPost, agent, &latest); err != nil {
			return err
		}
		if latest.BootImageSpec != current.BootImageSpec || latest.Host != current.Host ||
			latest.Generation != current.Generation || latest.Revision != current.Revision {
			return errors.New("KVM host image check changed during publication")
		}
		return nil
	}
	report := kvm.HostImageReport{AgentIdentity: agent, Revision: current.Revision, State: "verified", SHA256: current.SHA256, SizeBytes: current.SizeBytes}
	var receipt kvm.HostImageReceipt
	if err := mirror.publish(ctx, current.BootImageSpec, authorize, api.transport); err != nil {
		if current.State == "verified" && (errors.Is(err, errKVMImageMismatch) || errors.Is(err, errKVMUnsafeTarget)) {
			report.State = "missing"
			err = errors.Join(err, api.request(ctx, imageURL, http.MethodPut, report, &receipt))
		}
		return err
	}
	if err := api.request(ctx, imageURL, http.MethodPut, report, &receipt); err != nil {
		return err
	}
	if receipt.Host != current.Host || receipt.Generation != current.Generation || receipt.BootImageSpec != current.BootImageSpec ||
		receipt.Revision != current.Revision+1 || receipt.State != "verified" || !receipt.Available || receipt.ExpiresAt == nil {
		return errors.New("KVM host image publication receipt did not match")
	}
	return json.NewEncoder(output).Encode(receipt)
}
