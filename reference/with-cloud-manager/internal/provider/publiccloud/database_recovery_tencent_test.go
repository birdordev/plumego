package publiccloud

import (
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

func TestTencentRecoveryTimeRejectsAmbiguousWallClock(t *testing.T) {
	const source = "cdb-tzfixture"
	c := &Client{name: provider.TencentCVM, profile: Config{
		DatabaseBackupTimeZones: map[string]string{source: "America/New_York"},
	}}
	spec := provider.DatabaseRecovery{SourceID: source}
	for _, hour := range []int{5, 6} {
		spec.RestoreTime = time.Date(2025, time.November, 2, hour, 15, 0, 0, time.UTC)
		if stamp, err := c.tencentRecoveryTime(spec); stamp != "" || !provider.IsFailure(err, provider.CreationBlocked) {
			t.Fatalf("ambiguous restore point at hour %d was accepted: %q %v", hour, stamp, err)
		}
	}
	spec.RestoreTime = time.Date(2025, time.November, 2, 7, 15, 0, 0, time.UTC)
	if stamp, err := c.tencentRecoveryTime(spec); stamp != "2025-11-02 02:15:00" || err != nil {
		t.Fatalf("unambiguous restore point was not converted: %q %v", stamp, err)
	}
}
