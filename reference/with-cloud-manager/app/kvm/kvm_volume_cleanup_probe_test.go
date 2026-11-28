package kvmapp

import (
	"testing"
)

func TestKVMVolumeCleanupInventory(t *testing.T) {
	const header = " Name       Path\n-------------------------\n"
	const volume = "/srv/tenant pool/disk.raw"
	for _, sample := range []struct {
		row      string
		present  bool
		rejected bool
	}{
		{"", false, false},
		{" alias      " + volume, true, false},
		{" disk.raw   unknown", true, false},
		{" alias      unknown", false, true},
		{" other.raw  /srv/tenant pool/other.raw", false, false},
		{" alias      /srv/tenant pool/../wrong", false, true},
	} {
		present, err := kvmCleanupVolumeListed([]byte(header+sample.row), "disk.raw", volume)
		if present != sample.present || (err != nil) != sample.rejected {
			t.Fatalf("unexpected inventory result for %q: %t %v", sample.row, present, err)
		}
	}
}
