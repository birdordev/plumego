package kvmapp

import (
	"context"
	"path/filepath"
	"strings"

	"with-cloud-manager/internal/kvm"
)

type kvmCleanupPool struct {
	path, uuid, digest string
}

func readKVMCleanupPool(ctx context.Context, probe KVMProbe, name string) (kvmCleanupPool, error) {
	path, err := probe.poolPath(ctx, name)
	if err != nil {
		return kvmCleanupPool{}, err
	}
	if path == "" || path == "/" {
		return kvmCleanupPool{}, ErrKVMUnsafeTarget
	}
	if _, err := probe.poolFreeMiB(path); err != nil {
		return kvmCleanupPool{}, err
	}
	raw, err := probe.Read(ctx, "pool-uuid", name)
	if err != nil {
		return kvmCleanupPool{}, err
	}
	uuid := strings.TrimSpace(string(raw))
	if !kvmMachineUUID.MatchString(uuid) {
		return kvmCleanupPool{}, ErrKVMUnsafeTarget
	}
	digest, err := kvmCleanupPoolSHA256(path, uuid)
	return kvmCleanupPool{path: path, uuid: uuid, digest: digest}, err
}

func checkKVMCleanupReferences(ctx context.Context, probe KVMProbe, disk kvm.VM, pool kvmCleanupPool) error {
	// 清理必须扫描该 VM 自身的持久和生效定义，以及其他域的卷和 backing chain。
	disk.ID = ""
	return verifyKVMVolumeExclusive(ctx, probe.Read, disk, filepath.Join(pool.path, disk.Volume))
}

func checkKVMCleanupGeometry(ctx context.Context, probe KVMProbe, work kvm.VolumeCleanupWork, pool kvmCleanupPool) error {
	size, err := readKVMRawVolume(ctx, probe.Read, work.Disk, pool.path)
	if err != nil {
		return err
	}
	if size != work.Backup.SizeBytes {
		return ErrKVMUnsafeTarget
	}
	return checkKVMCleanupReferences(ctx, probe, work.Disk, pool)
}

func kvmCleanupVolumeListed(raw []byte, expected, path string) (bool, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 || strings.Join(strings.Fields(lines[0]), " ") != "Name Path" ||
		strings.Trim(strings.TrimSpace(lines[1]), "-") != "" || strings.TrimSpace(lines[1]) == "" {
		return false, ErrKVMUnsafeTarget
	}
	for _, row := range lines[2:] {
		fields := strings.Fields(row)
		if len(fields) < 2 {
			return false, ErrKVMUnsafeTarget
		}
		if fields[0] == expected {
			return true, nil
		}
		listedPath := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(row), fields[0]))
		if !filepath.IsAbs(listedPath) || filepath.Clean(listedPath) != listedPath {
			return false, ErrKVMUnsafeTarget
		}
		if listedPath == path {
			return true, nil
		}
	}
	return false, nil
}
