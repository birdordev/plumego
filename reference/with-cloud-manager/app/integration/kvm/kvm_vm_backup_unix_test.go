//go:build linux || darwin

package kvmtest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
)

func TestKVMBackupPublication(t *testing.T) {
	data := []byte("immutable stopped disk")
	image := testKVMImage(data)
	for _, name := range []string{"revoked", "rewritten", "symbolic link", "replaced pool", "public cache", "replayed"} {
		t.Run(name, func(t *testing.T) {
			pool, destination := imageFixtureDir(t), imageFixtureDir(t)
			const volume = "retired-boot"
			path := filepath.Join(pool, volume)
			published := filepath.Join(destination, image.SHA256+".raw")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if name == "symbolic link" {
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(volume+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			if name == "public cache" {
				if err := os.WriteFile(published, data, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(published, 0644); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			revoked := errors.New("fixture revoked source authorization")
			authorize := func() error {
				calls++
				switch {
				case name == "revoked" && calls == 4:
					return revoked
				case name == "rewritten" && calls == 3:
					// 即使写入方保持长度和修改时间，第二遍读取仍会拒绝内容变化。
					if err := os.WriteFile(path, make([]byte, len(data)), 0600); err != nil {
						return err
					}
					return os.Chtimes(path, info.ModTime(), info.ModTime())
				case name == "replaced pool" && calls == 4:
					if err := os.Rename(pool, pool+".original"); err != nil {
						return err
					}
					return os.Mkdir(pool, 0700)
				}
				return nil
			}
			got, err := kvmapp.PublishKVMBackup(t.Context(), pool, destination, volume, image.ID, image.SizeBytes, authorize)
			if name == "replayed" {
				replay, replayErr := kvmapp.PublishKVMBackup(t.Context(), pool, destination, volume, image.ID, image.SizeBytes, authorize)
				if err != nil || replayErr != nil || got != image || replay != image {
					t.Fatalf("backup replay failed: %+v %+v %v %v", got, replay, err, replayErr)
				}
				fi, statErr := os.Stat(published)
				if statErr != nil || fi.Mode().Perm() != 0600 {
					t.Fatalf("invalid published backup: %v", statErr)
				}
				return
			}
			want := map[string]error{"revoked": revoked, "rewritten": kvmapp.ErrKVMImageMismatch, "symbolic link": kvmapp.ErrKVMUnsafeTarget,
				"replaced pool": kvmapp.ErrKVMUnsafeTarget, "public cache": kvmapp.ErrKVMUnsafeTarget}[name]
			if !errors.Is(err, want) {
				t.Fatalf("expected %v, got %v", want, err)
			}
			if name == "public cache" {
				return
			}
			if _, err := os.Stat(published); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsafe backup was published: %v", err)
			}
		})
	}
}
