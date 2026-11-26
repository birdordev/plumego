package cmdb

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestSnapshotRequiresCompleteVerifiedStream(t *testing.T) {
	project := Project{Tenant: "tenant", Project: "project"}
	captured := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
	var body bytes.Buffer
	writer := NewSnapshotWriter(&body, project)
	asset := inventory.CMDBAsset{
		Account: "account", Region: "ap-guangzhou", Kind: provider.Instance, ID: "ins-01",
		Provider: "tencent-cvm", Name: "待核验实例", Status: "Running", State: "missing_candidate",
		OwnerProject: "project", OwnershipRevision: 1, Links: []provider.Link{},
	}
	for _, err := range []error{writer.Start(captured), writer.Asset(asset), writer.Finish()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	records := bytes.Split(body.Bytes(), []byte{'\n'})
	cases := []struct {
		name    string
		project Project
		input   []byte
		want    error
	}{
		{"complete", project, body.Bytes(), nil},
		{"wrong project", Project{Tenant: project.Tenant, Project: "other"}, body.Bytes(), ErrInvalidSnapshot},
		{"missing footer", project, bytes.Join(append(records[:2:2], nil), []byte{'\n'}), ErrInvalidSnapshot},
		{"bad checksum", project, bytes.Replace(body.Bytes(), []byte("ins-01"), []byte("ins-99"), 1), ErrInvalidSnapshot},
		{"bad count", project, bytes.Replace(body.Bytes(), []byte(`"count":1`), []byte(`"count":2`), 1), ErrInvalidSnapshot},
		{"unknown field", project, bytes.Replace(body.Bytes(), []byte(`"account":`), []byte(`"credential":"fixture","account":`), 1), ErrInvalidSnapshot},
		{"extra line", project, append(bytes.Clone(body.Bytes()), '\n'), ErrInvalidSnapshot},
		{"overlong line", project, bytes.Repeat([]byte{'{'}, maxLineBytes+1), ErrSnapshotLimit},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var seen []inventory.CMDBAsset
			manifest, summary, err := readSnapshot(bytes.NewReader(tt.input), tt.project, func(asset inventory.CMDBAsset, _ int) error {
				seen = append(seen, asset)
				return nil
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("snapshot result: got %v want %v", err, tt.want)
			}
			if tt.want == nil && (!manifest.CapturedAt.Equal(captured) ||
				summary.Count != 1 || len(seen) != 1 || seen[0].ID != asset.ID || seen[0].State != asset.State) {
				t.Fatalf("wrong complete snapshot: %+v %+v %+v", manifest, summary, seen)
			}
		})
	}
}
