package publiccloud

import (
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestSnapshotLookupRequiresUniqueSourceEvidence(t *testing.T) {
	const source = "disk-source"
	spec := provider.SnapshotSpec{Name: "requested-snapshot", VolumeID: source}
	snapshot := func(id, disk string) provider.Object {
		return snapshotObject(id, spec.Name, "NORMAL", disk, "", "PRIVATE_SNAPSHOT", 40)
	}
	find := snapshotLookup{spec: spec}
	if err := find.accept(snapshot("snap-other", source), false, true); err != nil || find.object.ID != "" {
		t.Fatalf("an uncorrelated snapshot was selected: %+v, err=%v", find, err)
	}
	if err := find.accept(snapshot("snap-other", "disk-wrong"), true, true); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("an unrelated source produced valid evidence: %v", err)
	}
	if err := find.accept(snapshot("snap-expected", source), true, false); err != nil || find.object.ID != "snap-expected" || find.ready {
		t.Fatalf("a pending snapshot was not retained as pending: %+v, err=%v", find, err)
	}
	if err := find.accept(snapshot("snap-duplicate", source), true, true); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("duplicate correlation marker was accepted: %v", err)
	}
	find = snapshotLookup{spec: spec, id: "snap-expected"}
	if err := find.accept(snapshot("snap-other", source), true, true); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("snapshot outside the receipt ID was accepted: %v", err)
	}
}
