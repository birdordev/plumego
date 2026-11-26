package main

import (
	"context"
	"errors"
	"io"
	"net/url"

	"with-cloud-manager/internal/kvm"
)

func runKVMShutdownJob(ctx context.Context, api kvmVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, output io.Writer, runner kvmVMRunner) error {
	state, err := inspectShutdownDomain(ctx, runner.read, job)
	if err != nil {
		return err
	}
	if job.State == "stopping" {
		return submitKVMObservation(ctx, api, target, agent, job, state, output)
	}
	if state != "running" && state != "defined" {
		return errors.Join(errKVMUnsafeTarget, submitKVMObservation(ctx, api, target, agent, job, state, output))
	}
	job, err = armKVMJob(ctx, api, target, agent, job, "stopping")
	if err != nil {
		return err
	}

	state, err = inspectShutdownDomain(ctx, runner.read, job)
	if err != nil {
		return err
	}
	var writeErr error
	if state == "running" {
		_, writeErr = runner.write(ctx, "shutdown", job.ID, "--mode", "acpi")
		state, err = inspectShutdownDomain(ctx, runner.read, job)
		if err != nil {
			return errors.Join(writeErr, err)
		}
	}
	return errors.Join(writeErr, submitKVMObservation(ctx, api, target, agent, job, state, output))
}

func inspectShutdownDomain(ctx context.Context, read virshReader, job kvm.VM) (string, error) {
	observed, err := inspectPersistentKVMVM(ctx, read, job)
	if err == nil && observed.state == "running" && !observed.acpi {
		return "mismatch", nil
	}
	return observed.state, err
}
