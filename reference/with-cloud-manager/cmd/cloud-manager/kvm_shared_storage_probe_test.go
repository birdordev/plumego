package main

import (
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestKVMMountInfo(t *testing.T) {
	source := kvm.SharedStorageSource{Server: "10.24.0.9", Export: "/kvm/compute"}
	const path = "/mnt/kvm pool"
	const valid = `64 25 0:41 / /mnt/kvm\040pool rw,relatime shared:2 - nfs4 10.24.0.9:/kvm/compute rw,vers=4.2,proto=tcp,addr=10.24.0.9,local_lock=none`
	if err := verifyKVMMountInfo([]byte(valid), path, source); err != nil {
		t.Fatal(err)
	}
	for _, example := range []struct{ name, candidate string }{
		{"wrong export", strings.Replace(valid, "/kvm/compute", "/kvm/other", 1)},
		{"wrong peer", strings.Replace(valid, "addr=10.24.0.9", "addr=10.24.0.8", 1)},
		{"read only", strings.Replace(valid, "rw,relatime", "ro,relatime", 1)},
		{"local lock", strings.Replace(valid, "local_lock=none", "local_lock=flock", 1)},
		{"v3", strings.Replace(valid, "vers=4.2", "vers=3", 1)},
		{"unexpected port", valid + ",port=12049"},
		{"bind mount", strings.Replace(valid, "0:41 / ", "0:41 /child ", 1)},
		{"stacked mounts", valid + "\n" + valid},
		{"nested mount", valid + "\n" + strings.Replace(valid, `/mnt/kvm\040pool`, `/mnt/kvm\040pool/.cmp-image-stage`, 1)},
		{"missing mount", ""},
	} {
		t.Run(example.name, func(t *testing.T) {
			if err := verifyKVMMountInfo([]byte(example.candidate), path, source); err == nil {
				t.Fatal("accepted mismatched shared storage mount")
			}
		})
	}
}
