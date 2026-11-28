package kvmapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"with-cloud-manager/internal/kvm"
)

var ErrKVMImageMismatch = errors.New("KVM boot image size or SHA-256 mismatch")

func validKVMImageSpec(vm kvm.VM) bool {
	if vm.ImageID == "" {
		return vm.ImageSHA256 == "" && vm.ImageSizeBytes == 0
	}
	image := kvm.BootImageSpec{ID: vm.ImageID, SHA256: vm.ImageSHA256, SizeBytes: vm.ImageSizeBytes}
	return image.Valid() && vm.Volume == kvm.ImageVolumeName(vm.Placement) && image.SizeBytes <= vm.Capacity.DiskMiB*(1<<20)
}

func runKVMImageStage(ctx context.Context, api KVMVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, profile KVMAgentProfile, output io.Writer, runner KVMVMRunner) error {
	if profile.ImageRoot == "" {
		return ErrKVMUnsafeTarget
	}
	if err := checkKVMBeforeWrite(ctx, runner.Read, job, "absent"); err != nil {
		return err
	}
	if err := validateKVMNetwork(ctx, runner.Read, job); err != nil {
		return err
	}
	pool, err := runner.poolPath(ctx, job.StoragePool)
	if err != nil {
		return err
	}
	if pool == "" {
		return ErrKVMUnsafeTarget
	}
	if _, err := runner.poolFreeMiB(pool); err != nil {
		return err
	}
	if err := verifyKVMVolumeExclusive(ctx, runner.Read, job, filepath.Join(pool, job.Volume)); err != nil {
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
			return ErrKVMUnsafeTarget
		}
		if job.SharedStoragePool != "" {
			path, err := runner.poolPath(ctx, job.StoragePool)
			if err != nil || path != pool {
				return errors.Join(ErrKVMUnsafeTarget, err)
			}
		}
		return nil
	}
	if err := authorize(); err != nil {
		return err
	}
	if err := StageKVMImage(ctx, profile.ImageRoot, pool, job, authorize); err != nil {
		if errors.Is(err, ErrKVMImageMismatch) {
			return errors.Join(err, submitKVMObservation(ctx, api, target, agent, job, "mismatch", output))
		}
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	if _, err := runner.Write(ctx, "pool-refresh", job.StoragePool); err != nil {
		return err
	}
	if err := validateKVMVolume(ctx, runner, job); err != nil {
		return err
	}
	return submitKVMObservation(ctx, api, target, agent, job, "staged", output)
}
