package kvmapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmVolumeCleanupTarget struct {
	host, placement, id string
	generation          int64
}

type kvmVolumeCleanupAgent struct {
	api     KVMVMAPI
	target  *url.URL
	host    kvm.Host
	probe   KVMProbe
	write   VirshReader
	profile KVMAgentProfile
}

func ParseKVMVolumeCleanupTarget(args []string) (kvmVolumeCleanupTarget, bool) {
	if len(args) != 8 || !kvmUnitName.MatchString(args[4]) || !kvmSafePlacement.MatchString(args[6]) || !kvmUnitName.MatchString(args[7]) {
		return kvmVolumeCleanupTarget{}, false
	}
	generation, err := strconv.ParseInt(args[5], 10, 64)
	if err != nil || generation < 1 {
		return kvmVolumeCleanupTarget{}, false
	}
	return kvmVolumeCleanupTarget{host: args[4], placement: args[6], id: args[7], generation: generation}, true
}

func RunKVMVolumeCleanup(ctx context.Context, api KVMVMAPI, target kvmVolumeCleanupTarget, input io.Reader,
	output io.Writer, probe KVMProbe, write VirshReader) error {
	profile, err := readKVMAgentProfile(input)
	if err != nil {
		return err
	}
	if profile.ImageRoot == "" {
		return ErrKVMUnsafeTarget
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.Target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Generation != target.generation || host.FenceState != "" || !kvmMachineUUID.MatchString(host.UUID) {
		return ErrKVMUnsafeTarget
	}
	caps, err := probe.Read(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(caps, host.UUID); err != nil {
		return err
	}
	agent := kvmVolumeCleanupAgent{api: api, target: api.Target.JoinPath("volume-cleanups", target.placement, target.id),
		host: host, probe: probe, write: write, profile: profile}
	work, err := agent.command(ctx, kvm.VolumeCleanupCommand{Action: "inspect"})
	if err != nil {
		return err
	}
	if !agent.matches(target, work) {
		return ErrKVMUnsafeTarget
	}
	if work.Cleanup.Status != "approved" && work.Cleanup.Status != "dispatched" {
		return json.NewEncoder(output).Encode(work.Cleanup)
	}
	if work.Disk.ImageID == "" && !slices.Contains(profile.Volumes, work.Disk.Volume) {
		return ErrKVMUnsafeTarget
	}
	agent.probe.shared, err = loadKVMSharedStorage(ctx, api, host)
	if err != nil {
		return err
	}
	pool, err := readKVMCleanupPool(ctx, agent.probe, work.Disk.StoragePool)
	if err != nil {
		return err
	}
	var result kvm.VolumeCleanupWork
	if work.Cleanup.Status == "approved" {
		result, err = agent.dispatch(ctx, work, pool)
	} else {
		result, err = agent.observe(ctx, work, pool)
	}
	if result.Cleanup.ID == "" {
		return err
	}
	return errors.Join(err, json.NewEncoder(output).Encode(result.Cleanup))
}

func (agent kvmVolumeCleanupAgent) matches(target kvmVolumeCleanupTarget, work kvm.VolumeCleanupWork) bool {
	job, disk := work.Cleanup, work.Disk
	if job.ID != target.id || job.Placement != target.placement || job.Host != target.host || job.HostGeneration != target.generation ||
		disk.ID != job.VM || !kvmMachineUUID.MatchString(disk.ID) || disk.Placement != job.Placement || disk.Host != job.Host ||
		disk.Generation < 1 || disk.Generation > target.generation || disk.StoragePool != agent.host.StoragePool ||
		disk.SharedStoragePool != agent.host.SharedStoragePool || !kvmUnitName.MatchString(disk.StoragePool) ||
		!kvmUnitName.MatchString(disk.Volume) || !work.Backup.Valid() || work.Backup.ID != job.Backup ||
		disk.Capacity.DiskMiB < 1 || disk.Capacity.DiskMiB > 1<<40 || work.Backup.SizeBytes > disk.Capacity.DiskMiB*(1<<20) {
		return false
	}
	if disk.ImageID == "" {
		return disk.SharedStoragePool == "" && !strings.HasPrefix(disk.Volume, "cmp-image-")
	}
	return kvmUnitName.MatchString(disk.ImageID) && disk.Volume == kvm.ImageVolumeName(disk.Placement) && disk.ImageSizeBytes == work.Backup.SizeBytes
}

func (agent kvmVolumeCleanupAgent) command(ctx context.Context, command kvm.VolumeCleanupCommand) (kvm.VolumeCleanupWork, error) {
	command.AgentIdentity = kvm.AgentIdentity{UUID: agent.host.UUID, Generation: agent.host.Generation}
	var work kvm.VolumeCleanupWork
	err := agent.api.request(ctx, agent.target, http.MethodPost, command, &work)
	if err == nil && work.Cleanup.ID == "" {
		return work, ErrKVMUnsafeTarget
	}
	return work, err
}

func (agent kvmVolumeCleanupAgent) dispatch(ctx context.Context, work kvm.VolumeCleanupWork, pool kvmCleanupPool) (kvm.VolumeCleanupWork, error) {
	if !distinctKVMCleanupDirectories(pool.path, agent.profile.ImageRoot) {
		return work, ErrKVMUnsafeTarget
	}
	if err := checkKVMCleanupGeometry(ctx, agent.probe, work, pool); err != nil {
		return work, err
	}
	backupName := work.Backup.SHA256 + ".raw"
	backupFile, err := hashKVMCleanupFile(ctx, agent.profile.ImageRoot, backupName, work.Backup)
	if err != nil {
		return work, err
	}
	oldFile, err := hashKVMCleanupFile(ctx, pool.path, work.Disk.Volume, work.Backup)
	if err != nil {
		return work, err
	}
	armed, err := agent.command(ctx, kvm.VolumeCleanupCommand{Action: "arm", Revision: work.Cleanup.Revision, PoolSHA256: pool.digest})
	if err != nil {
		return work, err
	}
	if armed.Cleanup.ID != work.Cleanup.ID || armed.Cleanup.Revision != work.Cleanup.Revision+1 || armed.Cleanup.Status != "dispatched" ||
		armed.Cleanup.PoolSHA256 != pool.digest || armed.Disk != work.Disk || armed.Backup != work.Backup {
		return armed, ErrKVMUnsafeTarget
	}
	currentPool, err := readKVMCleanupPool(ctx, agent.probe, armed.Disk.StoragePool)
	if err != nil || currentPool != pool {
		return armed, errors.Join(ErrKVMUnsafeTarget, err)
	}
	if err := checkKVMCleanupGeometry(ctx, agent.probe, armed, pool); err != nil {
		return armed, err
	}
	verified, err := agent.command(ctx, kvm.VolumeCleanupCommand{Action: "verify", Revision: armed.Cleanup.Revision, PoolSHA256: pool.digest})
	if err != nil || verified.Disk != armed.Disk || verified.Backup != armed.Backup || verified.Cleanup.Revision != armed.Cleanup.Revision ||
		verified.Cleanup.ID != armed.Cleanup.ID || verified.Cleanup.Status != "dispatched" {
		return armed, errors.Join(ErrKVMUnsafeTarget, err)
	}
	if err := checkKVMCleanupFile(agent.profile.ImageRoot, backupName, backupFile); err != nil {
		return armed, err
	}
	if err := checkKVMCleanupFile(pool.path, work.Disk.Volume, oldFile); err != nil {
		return armed, err
	}
	_, deleteErr := agent.write(ctx, "vol-delete", "--pool", pool.uuid, armed.Disk.Volume)
	result, observeErr := agent.observe(ctx, armed, pool)
	return result, errors.Join(deleteErr, observeErr)
}

func (agent kvmVolumeCleanupAgent) observe(ctx context.Context, work kvm.VolumeCleanupWork, pool kvmCleanupPool) (kvm.VolumeCleanupWork, error) {
	if pool.digest != work.Cleanup.PoolSHA256 {
		return work, ErrKVMUnsafeTarget
	}
	local, err := readKVMCleanupPool(ctx, agent.probe, work.Disk.StoragePool)
	if err != nil || local != pool {
		return work, errors.Join(ErrKVMUnsafeTarget, err)
	}
	_, present, err := kvmCleanupFileInfo(pool.path, work.Disk.Volume)
	if err != nil {
		return work, err
	}
	if !present {
		present, err = agent.refreshVolumePresence(ctx, work, pool)
		if err != nil {
			return work, err
		}
	}
	action := "present"
	if !present {
		action = "absent"
	}
	return agent.command(ctx, kvm.VolumeCleanupCommand{Action: action, Revision: work.Cleanup.Revision, PoolSHA256: pool.digest})
}

func (agent kvmVolumeCleanupAgent) refreshVolumePresence(ctx context.Context, work kvm.VolumeCleanupWork, pool kvmCleanupPool) (bool, error) {
	if _, err := agent.write(ctx, "pool-refresh", pool.uuid); err != nil {
		return false, err
	}
	raw, err := agent.probe.Read(ctx, "vol-list", "--pool", pool.uuid)
	if err != nil {
		return false, err
	}
	listed, err := kvmCleanupVolumeListed(raw, work.Disk.Volume, filepath.Join(pool.path, work.Disk.Volume))
	if err != nil || listed {
		return listed, err
	}
	if err := checkKVMCleanupReferences(ctx, agent.probe, work.Disk, pool); err != nil {
		return false, err
	}
	latest, err := readKVMCleanupPool(ctx, agent.probe, work.Disk.StoragePool)
	if err != nil || latest != pool {
		return false, errors.Join(ErrKVMUnsafeTarget, err)
	}
	_, present, err := kvmCleanupFileInfo(pool.path, work.Disk.Volume)
	return present, err
}
