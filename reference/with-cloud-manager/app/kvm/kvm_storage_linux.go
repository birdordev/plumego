package kvmapp

import (
	"errors"
	"math"
	"os"
	"syscall"
)

func kvmLocalFreeMiB(path string) (int64, error) {
	return kvmFilesystemFreeMiB(path, false)
}

func kvmSharedFreeMiB(path string) (int64, error) {
	return kvmFilesystemFreeMiB(path, true)
}

func kvmFilesystemFreeMiB(path string, shared bool) (int64, error) {
	var info syscall.Statfs_t
	if err := syscall.Statfs(path, &info); err != nil {
		return 0, errors.New("cannot read local KVM storage capacity")
	}
	const (
		ext4  = 0xef53
		xfs   = 0x58465342
		btrfs = 0x9123683e
		nfs   = 0x6969
	)
	filesystem := uint32(info.Type)
	if shared && filesystem != nfs {
		return 0, errors.New("KVM shared storage must use NFS")
	}
	if !shared && filesystem != ext4 && filesystem != xfs && filesystem != btrfs {
		return 0, errors.New("KVM directory pool must use a supported local filesystem")
	}
	blockSize := info.Frsize
	if blockSize <= 0 || info.Bavail > math.MaxUint64/uint64(blockSize) {
		return 0, errors.New("invalid KVM filesystem capacity")
	}
	return int64(info.Bavail * uint64(blockSize) / (1 << 20)), nil
}

func kvmLocalBootFile(name string, size int64) error {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return ErrKVMUnsafeTarget
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return ErrKVMUnsafeTarget
	}
	return nil
}
