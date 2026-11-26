package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

const snapshotIntentTag = "plumego-cmp-snapshot"

func (c *Client) checkSnapshotScope(scope provider.Scope, spec provider.SnapshotSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Snapshot || !slices.Contains(c.profile.Regions, scope.Region) ||
		!launchTokenPattern.MatchString(token) || !spec.ValidFor(c.name) {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func snapshotMarker(scope provider.Scope, token string) string {
	return sha([]byte(snapshotIntentTag + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func (c *Client) PrepareSnapshotCreation(ctx context.Context, scope provider.Scope, spec provider.SnapshotSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkSnapshotScope(scope, spec, token); err != nil {
		return nil, err
	}
	if !c.profile.AllowSnapshotCreation {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	available, attached, err := c.snapshotVolumeState(ctx, scope.Region, spec.VolumeID)
	if err != nil {
		return nil, err
	}
	if !available && (!attached || !spec.AllowAttached) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	marker := snapshotMarker(scope, token)
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.createTencentSnapshot(ctx, scope.Region, spec, marker)
		case provider.AlibabaECS:
			return c.createAlibabaSnapshot(ctx, scope.Region, spec, token, marker)
		default:
			return c.createHuaweiSnapshot(ctx, scope.Region, spec, marker, attached)
		}
	}, nil
}

func (c *Client) snapshotVolumeState(ctx context.Context, region, id string) (available, attached bool, err error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct{ DiskIds []string }{[]string{id}})
		if err != nil {
			return false, false, err
		}
		var result struct {
			DiskSet []struct {
				DiskId, DiskState string
				SnapshotAbility   *bool
			}
		}
		if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &result); err != nil {
			return false, false, err
		}
		if len(result.DiskSet) != 1 || result.DiskSet[0].DiskId != id {
			return false, false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		disk := result.DiskSet[0]
		if disk.SnapshotAbility == nil || !*disk.SnapshotAbility {
			return false, false, &provider.Failure{Kind: provider.CreationBlocked}
		}
		return disk.DiskState == "UNATTACHED", disk.DiskState == "ATTACHED", nil
	case provider.AlibabaECS:
		ids, err := json.Marshal([]string{id})
		if err != nil {
			return false, false, err
		}
		rows, err := c.describeAlibabaDisks(ctx, region, url.Values{"DiskIds": {string(ids)}})
		if err != nil {
			return false, false, err
		}
		if len(rows) != 1 || rows[0].DiskId != id {
			return false, false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		disk := rows[0]
		if disk.RegionId != region {
			return false, false, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !disk.cloudDisk() {
			return false, false, &provider.Failure{Kind: provider.CreationBlocked}
		}
		return disk.Status == "Available", disk.Status == "In_use", nil
	default:
		endpoint := huaweiEVS(region, "/v2/"+c.profile.ProjectID+"/cloudvolumes/"+id)
		var result struct {
			Volume struct {
				ID, Status string
				Project    string `json:"os-vol-tenant-attr:tenant_id"`
			}
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return false, false, err
		}
		if result.Volume.Project != c.profile.ProjectID {
			return false, false, &provider.Failure{Kind: provider.Unauthorized}
		}
		if result.Volume.ID != id {
			return false, false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return result.Volume.Status == "available", result.Volume.Status == "in-use", nil
	}
}

func (c *Client) createTencentSnapshot(ctx context.Context, region string, spec provider.SnapshotSpec, marker string) (provider.LaunchReceipt, error) {
	body, err := json.Marshal(struct {
		DiskId, SnapshotName string
		Tags                 []tag
	}{spec.VolumeID, spec.Name, []tag{{Key: snapshotIntentTag, Value: marker}}})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var response struct{ SnapshotId, RequestId string }
	if err := c.tencent(ctx, "cbs", region, "CreateSnapshot", "2017-03-12", body, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt([]string{response.SnapshotId}, response.RequestId, "snap-")
}

func (c *Client) createAlibabaSnapshot(ctx context.Context, region string, spec provider.SnapshotSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "DiskId": {spec.VolumeID}, "SnapshotName": {spec.Name}, "ClientToken": {token},
		"Tag.1.Key": {snapshotIntentTag}, "Tag.1.Value": {marker}}
	var response struct{ Code, SnapshotId, RequestId string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "CreateSnapshot", "2014-05-26", query, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if response.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(response.Code)
	}
	return creationReceipt([]string{response.SnapshotId}, response.RequestId, "s-")
}

func huaweiEVS(region, path string) url.URL {
	return url.URL{Scheme: "https", Host: "evs." + region + ".myhuaweicloud.com", Path: path}
}

func (c *Client) createHuaweiSnapshot(ctx context.Context, region string, spec provider.SnapshotSpec, marker string, attached bool) (provider.LaunchReceipt, error) {
	payload := struct {
		Snapshot struct {
			VolumeID    string            `json:"volume_id"`
			Name        string            `json:"name"`
			Description string            `json:"description,omitempty"`
			Metadata    map[string]string `json:"metadata,omitempty"`
			Force       *bool             `json:"force,omitempty"`
		} `json:"snapshot"`
	}{}
	payload.Snapshot.VolumeID = spec.VolumeID
	payload.Snapshot.Name = spec.Name
	path := "/v5/" + c.profile.ProjectID + "/snapshots"
	if c.profile.HuaweiSnapshotAPI != "v5" {
		payload.Snapshot.Force = &attached
		payload.Snapshot.Metadata = map[string]string{snapshotIntentTag: marker}
		path = "/v2/" + c.profile.ProjectID + "/cloudsnapshots"
	} else {
		payload.Snapshot.Description = snapshotIntentTag + ":" + marker
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := huaweiEVS(region, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var response struct {
		JobID      string `json:"job_id"`
		SnapshotID string `json:"snapshot_id"`
		Snapshot   struct {
			ID       string `json:"id"`
			VolumeID string `json:"volume_id"`
			Project  string `json:"os-extended-snapshot-attributes:project_id"`
		} `json:"snapshot"`
	}
	if err := c.request(req, &response, http.StatusAccepted); err != nil {
		return provider.LaunchReceipt{}, err
	}
	id := response.SnapshotID
	if c.profile.HuaweiSnapshotAPI != "v5" {
		if (response.Snapshot.VolumeID != "" && response.Snapshot.VolumeID != spec.VolumeID) ||
			(response.Snapshot.Project != "" && response.Snapshot.Project != c.profile.ProjectID) {
			return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		id = response.Snapshot.ID
	}
	if !huaweiResourceID.MatchString(id) {
		return provider.LaunchReceipt{RequestID: response.JobID}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.LaunchReceipt{RequestID: response.JobID, ResourceID: id}, nil
}

type snapshotLookup struct {
	spec       provider.SnapshotSpec
	id, marker string
	object     provider.Object
	ready      bool
}

func (find *snapshotLookup) accept(object provider.Object, marked, ready bool) error {
	if find.id != "" && object.ID != find.id {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if !marked {
		return nil
	}
	if find.object.ID != "" || object.Name != find.spec.Name || !object.ValidSnapshot() ||
		len(object.Links) == 0 || object.Links[0] != (provider.Link{Kind: provider.Volume, ID: find.spec.VolumeID, Relation: "snapshot_of"}) {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	find.object, find.ready = object, ready
	return nil
}

func (c *Client) FindCreatedSnapshot(ctx context.Context, scope provider.Scope, spec provider.SnapshotSpec, token, id string) (provider.Object, error) {
	if err := c.checkSnapshotScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	read := c.huaweiSnapshots
	validID := huaweiResourceID.MatchString
	switch c.name {
	case provider.TencentCVM:
		read = c.tencentSnapshots
		validID = func(id string) bool { return validLaunchID(id, "snap-") }
	case provider.AlibabaECS:
		read = c.alibabaSnapshots
		validID = func(id string) bool { return validLaunchID(id, "s-") }
	}
	if id != "" && !validID(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	find := snapshotLookup{spec: spec, id: id, marker: snapshotMarker(scope, token)}
	var cursor string
	for range 8 {
		page, err := read(ctx, scope.Region, cursor, &find)
		if err != nil {
			return provider.Object{}, err
		}
		if err := page.Validate(cursor); err != nil {
			return provider.Object{}, err
		}
		if id != "" && len(page.Objects) > 1 {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if page.Next == "" {
			if !find.ready {
				return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
			}
			return find.object, nil
		}
		cursor = page.Next
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}
