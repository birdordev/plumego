package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"with-cloud-manager/internal/kvm"
)

func validKVMImageSpec(vm kvm.VM) bool {
	if vm.ImageID == "" {
		return vm.ImageSHA256 == "" && vm.ImageSizeBytes == 0
	}
	image := kvm.BootImageSpec{ID: vm.ImageID, SHA256: vm.ImageSHA256, SizeBytes: vm.ImageSizeBytes}
	return image.Valid() && vm.Volume == kvm.ImageVolumeName(vm.Placement) && image.SizeBytes <= vm.Capacity.DiskMiB*(1<<20)
}

func runKVMImageStage(ctx context.Context, api kvmVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, profile kvmAgentProfile, output io.Writer, runner kvmVMRunner) error {
	if profile.ImageRoot == "" {
		return errKVMUnsafeTarget
	}
	if err := checkKVMBeforeWrite(ctx, runner.read, job, "absent"); err != nil {
		return err
	}
	if err := validateKVMNetwork(ctx, runner.read, job); err != nil {
		return err
	}
	pool, err := runner.poolPath(ctx, job.StoragePool)
	if err != nil {
		return err
	}
	if pool == "" {
		return errKVMUnsafeTarget
	}
	if _, err := runner.freeMiB(pool); err != nil {
		return err
	}
	if err := verifyKVMVolumeExclusive(ctx, runner.read, job, filepath.Join(pool, job.Volume)); err != nil {
		return err
	}
	if job.State == "queued" {
		job, err = armKVMJob(ctx, api, target, agent, job, "staging")
		if err != nil {
			return err
		}
	}
	command := kvm.VMCommand{AgentIdentity: agent, Revision: job.Revision}
	authorize := func() error {
		var current kvm.VM
		if err := api.request(ctx, target.JoinPath("image-plan"), http.MethodPost, command, &current); err != nil {
			return err
		}
		if current != job {
			return errKVMUnsafeTarget
		}
		return nil
	}
	if err := authorize(); err != nil {
		return err
	}
	if err := stageKVMImage(ctx, profile.ImageRoot, pool, job, authorize); err != nil {
		return errors.Join(err, submitKVMObservation(ctx, api, target, agent, job, "mismatch", output))
	}
	if err := authorize(); err != nil {
		return err
	}
	if _, err := runner.write(ctx, "pool-refresh", job.StoragePool); err != nil {
		return err
	}
	if err := validateKVMVolume(ctx, runner, job); err != nil {
		return err
	}
	return submitKVMObservation(ctx, api, target, agent, job, "staged", output)
}
