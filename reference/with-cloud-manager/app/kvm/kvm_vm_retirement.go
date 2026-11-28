package kvmapp

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"

	"with-cloud-manager/internal/kvm"
)

func runKVMRetirementJob(ctx context.Context, api KVMVMAPI, target *url.URL, agent kvm.AgentIdentity, job kvm.VM, output io.Writer, runner KVMVMRunner) error {
	if job.State == "undefining" {
		return observeKVMJob(ctx, api, target, agent, job, output, runner.Read)
	}
	state, err := inspectRetirementDomain(ctx, runner.Read, job)
	if err != nil {
		return err
	}
	if state != "defined" {
		return errors.Join(ErrKVMUnsafeTarget, submitKVMObservation(ctx, api, target, agent, job, state, output))
	}
	job, err = armKVMJob(ctx, api, target, agent, job, "undefining")
	if err != nil {
		return err
	}
	state, err = inspectRetirementDomain(ctx, runner.Read, job)
	if err != nil {
		return err
	}
	if state != "defined" {
		return errors.Join(ErrKVMUnsafeTarget, submitKVMObservation(ctx, api, target, agent, job, state, output))
	}
	// 仅移除域定义。libvirt 自身的依赖保护仍生效。
	_, writeErr := runner.Write(ctx, "undefine", job.ID)
	return errors.Join(writeErr, observeKVMJob(ctx, api, target, agent, job, output, runner.Read))
}

func inspectRetirementDomain(ctx context.Context, read VirshReader, job kvm.VM) (string, error) {
	observed, err := inspectPersistentKVMVM(ctx, read, job)
	if err != nil || observed.state != "defined" {
		return observed.state, err
	}
	if observed.retireProtected {
		return "mismatch", nil
	}
	info, err := read(ctx, "dominfo", job.ID)
	if err != nil {
		return "", err
	}
	if !kvmRetirementInfoMatches(string(info), job.ID) {
		return "mismatch", nil
	}
	for _, command := range []string{"snapshot-list", "checkpoint-list"} {
		data, err := read(ctx, command, job.ID, "--name")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(data)) != "" {
			return "mismatch", nil
		}
	}
	return observed.state, nil
}

func kvmRetirementInfoMatches(info, uuid string) bool {
	fields := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, duplicate := fields[key]; duplicate {
			return false
		}
		fields[key] = strings.TrimSpace(value)
	}
	return fields["UUID"] == uuid && fields["State"] == "shut off" && fields["Persistent"] == "yes" &&
		fields["Managed save"] == "no" && fields["Autostart"] == "disable" &&
		(fields["Autostart Once"] == "" || fields["Autostart Once"] == "disable")
}
