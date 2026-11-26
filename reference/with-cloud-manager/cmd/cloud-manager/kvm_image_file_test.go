//go:build linux || darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func imageFixtureDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func testKVMImage(data []byte) kvm.BootImageSpec {
	digest := sha256.Sum256(data)
	return kvm.BootImageSpec{ID: "test-raw", SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(data))}
}

func TestKVMImageAtomicStaging(t *testing.T) {
	source, pool := imageFixtureDir(t), imageFixtureDir(t)
	data := []byte("raw guest image contents")
	image := testKVMImage(data)
	job := kvm.VM{Placement: "test-placement", ImageID: image.ID, ImageSHA256: image.SHA256,
		ImageSizeBytes: image.SizeBytes, Capacity: kvm.Capacity{DiskMiB: 1}}
	job.Volume = kvm.ImageVolumeName(job.Placement)
	sourcePath, target := filepath.Join(source, image.SHA256+".raw"), filepath.Join(pool, job.Volume)
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	approved := func() error { return nil }
	write(sourcePath, make([]byte, len(data)))
	if err := stageKVMImage(t.Context(), source, pool, job, approved); err == nil {
		t.Fatal("wrong image digest was staged")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched source published a target: %v", err)
	}
	write(sourcePath, data)
	requests := 0
	revokeBeforePublish := func() error {
		requests++
		if requests > 1 {
			return errors.New("authorization revoked")
		}
		return nil
	}
	if err := stageKVMImage(t.Context(), source, pool, job, revokeBeforePublish); err == nil {
		t.Fatal("revoked authorization allowed publish")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected publication acquired destination: %v", err)
	}

	requests = 0
	interruptAfterPublish := func() error {
		requests++
		if requests > 2 {
			return errors.New("interrupted after publication")
		}
		return nil
	}
	if err := stageKVMImage(t.Context(), source, pool, job, interruptAfterPublish); err == nil {
		t.Fatal("fixture did not stop after publication")
	}
	info, err := os.Stat(target)
	if err != nil || kvmImageLinkCount(info) != 2 {
		t.Fatalf("interrupted stage lacks repairable ownership evidence: %v", err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := stageKVMImage(t.Context(), source, pool, job, approved); err != nil {
		t.Fatal(err)
	}
	if err := verifyKVMImageVolume(t.Context(), pool, job); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(target)
	if err != nil || !os.SameFile(info, after) || kvmImageLinkCount(after) != 1 {
		t.Fatalf("retry replaced the existing file or left a duplicate link: %v", err)
	}
	write(target, make([]byte, len(data)))
	if err := stageKVMImage(t.Context(), source, pool, job, approved); err == nil {
		t.Fatal("different existing destination was accepted")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(make([]byte, len(data))) {
		t.Fatalf("stage overwrote an existing destination: %v", err)
	}
}

func TestKVMImageStageSymlinkAndCancellation(t *testing.T) {
	source, pool := imageFixtureDir(t), imageFixtureDir(t)
	data := []byte("known raw image")
	image := testKVMImage(data)
	job := kvm.VM{Placement: "test-placement", ImageID: image.ID, ImageSHA256: image.SHA256,
		ImageSizeBytes: image.SizeBytes, Capacity: kvm.Capacity{DiskMiB: 1}}
	job.Volume = kvm.ImageVolumeName(job.Placement)
	outside := filepath.Join(imageFixtureDir(t), "outside.raw")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, image.SHA256+".raw")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	approve := func() error { return nil }
	if err := stageKVMImage(t.Context(), source, pool, job, approve); err == nil {
		t.Fatal("linked source was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := stageKVMImage(ctx, source, pool, job, func() error {
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("stage lost cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pool, job.Volume)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled stage published a target: %v", err)
	}
}
