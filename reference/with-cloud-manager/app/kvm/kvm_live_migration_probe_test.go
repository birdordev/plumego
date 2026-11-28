package kvmapp

import (
	"testing"
)

func TestKVMLiveMigrationTLSAllowlist(t *testing.T) {
	if dest, err := parseKVMLiveDestination("qemu+tls://node-02.example.test:16514/system"); err != nil || dest.Hostname() != "node-02.example.test" {
		t.Fatalf("rejected a valid TLS endpoint: %v", err)
	}
	for _, raw := range []string{
		"qemu+tcp://host/system",
		"qemu+ssh://host/system",
		"qemu+tls://user@host/system",
		"qemu+tls://host/system?no_verify=1",
		"qemu+tls://host/system#",
		"qemu+tls://host/session",
		"qemu+tls://host:0/system",
		"qemu+tls://host:65536/system",
		"qemu+tls://host:/system",
	} {
		if dest, err := parseKVMLiveDestination(raw); err == nil {
			t.Fatalf("accepted disallowed endpoint: %s", dest)
		}
	}
}
