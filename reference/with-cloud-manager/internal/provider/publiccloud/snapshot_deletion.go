package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkSnapshotTargetScope(scope provider.Scope, target provider.SnapshotTarget) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Snapshot || !slices.Contains(c.profile.Regions, scope.Region) || !target.ValidFor(c.name) {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (c *Client) PrepareSnapshotDeletion(ctx context.Context, scope provider.Scope, target provider.SnapshotTarget) (func(context.Context) (string, error), error) {
	if err := c.checkSnapshotTargetScope(scope, target); err != nil {
		return nil, err
	}
	if !c.profile.AllowSnapshotDeletion {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	state, err := c.readSnapshotState(ctx, scope.Region, target)
	if err != nil {
		return nil, err
	}
	if !state.deletable {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.deleteSnapshot(ctx, scope.Region, target)
	}, nil
}

func (c *Client) ObserveSnapshotDeletion(ctx context.Context, scope provider.Scope, target provider.SnapshotTarget) (provider.ActionResult, error) {
	if err := c.checkSnapshotTargetScope(scope, target); err != nil {
		return provider.ActionPending, err
	}
	state, err := c.readSnapshotState(ctx, scope.Region, target)
	if err != nil || state.present {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}

type snapshotState struct {
	present, ready, deletable bool
	size                      int64
	zone                      string
	encrypted                 *bool
}

func (c *Client) readSnapshotState(ctx context.Context, region string, target provider.SnapshotTarget) (snapshotState, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.tencentSnapshotState(ctx, region, target)
	case provider.AlibabaECS:
		return c.alibabaSnapshotState(ctx, region, target)
	default:
		return c.huaweiSnapshotState(ctx, region, target)
	}
}

func (c *Client) tencentSnapshotState(ctx context.Context, region string, target provider.SnapshotTarget) (snapshotState, error) {
	query, err := json.Marshal(struct{ SnapshotIds []string }{[]string{target.ID}})
	if err != nil {
		return snapshotState{}, err
	}
	var result struct {
		TotalCount  *int
		SnapshotSet []struct {
			SnapshotId, SnapshotType, SnapshotState, DiskId, AutoSnapshotPolicyId string
			ImageCount, ShareReference                                            *int
			IsLocked                                                              *bool
			CopyFromRemote                                                        bool
			Images                                                                []json.RawMessage
			DiskSize                                                              int64
			DiskUsage                                                             string
			Encrypt                                                               *bool
			Placement                                                             struct{ Zone string }
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeSnapshots", "2017-03-12", query, &result); err != nil {
		return snapshotState{}, err
	}
	rows := result.SnapshotSet
	if rows == nil || len(rows) > 1 || result.TotalCount == nil || *result.TotalCount != len(rows) {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return snapshotState{}, nil
	}
	row := rows[0]
	if row.SnapshotId != target.ID || row.DiskId != target.VolumeID || row.SnapshotType != target.Class || row.CopyFromRemote || row.SnapshotState == "" {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	normal := row.SnapshotState == "NORMAL"
	return snapshotState{present: true,
		ready: normal && row.DiskUsage == "DATA_DISK",
		deletable: normal && row.IsLocked != nil && !*row.IsLocked && row.ImageCount != nil && *row.ImageCount == 0 &&
			len(row.Images) == 0 && row.ShareReference != nil && *row.ShareReference == 0 && row.AutoSnapshotPolicyId == "",
		size: row.DiskSize, zone: row.Placement.Zone, encrypted: row.Encrypt,
	}, nil
}

func (c *Client) alibabaSnapshotState(ctx context.Context, region string, target provider.SnapshotTarget) (snapshotState, error) {
	ids, err := json.Marshal([]string{target.ID})
	if err != nil {
		return snapshotState{}, err
	}
	query := url.Values{"RegionId": {region}, "SnapshotIds": {string(ids)}, "MaxResults": {"100"}}
	var result struct {
		Code, NextToken string
		TotalCount      *int
		Snapshots       struct {
			Snapshot []struct {
				SnapshotId, SourceDiskId, SourceStorageType, SourceRegionId, SourceSnapshotId string
				RegionId, Category, Status, Usage, SnapshotType                               string
				SourceDiskSize                                                                json.Number
				Available                                                                     json.RawMessage
				Encrypted                                                                     *bool
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSnapshots", "2014-05-26", query, &result); err != nil {
		return snapshotState{}, err
	}
	if result.Code != "" {
		return snapshotState{}, cloudFailure(result.Code)
	}
	rows := result.Snapshots.Snapshot
	if rows == nil || len(rows) > 1 || result.NextToken != "" || (result.TotalCount != nil && *result.TotalCount != len(rows)) {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return snapshotState{}, nil
	}
	row := rows[0]
	if row.RegionId != region || (row.SourceRegionId != "" && row.SourceRegionId != region) {
		return snapshotState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if row.SnapshotId != target.ID || row.SourceDiskId != target.VolumeID || row.Category != target.Class || row.SourceSnapshotId != "" ||
		(row.SourceStorageType != "" && row.SourceStorageType != "disk") || row.Status == "" {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	size, err := snapshotSize(row.SourceDiskSize)
	if err != nil {
		return snapshotState{}, err
	}
	completed := row.Status == "accomplished"
	return snapshotState{present: true, size: size, encrypted: row.Encrypted,
		ready:     completed && snapshotAvailable(row.Available),
		deletable: completed && row.Usage == "none" && row.SnapshotType == "user",
	}, nil
}

func (c *Client) huaweiSnapshotCollection(class string) string {
	if class == "standard" {
		return "/v5/" + c.profile.ProjectID + "/snapshots"
	}
	return "/v2/" + c.profile.ProjectID + "/cloudsnapshots"
}

func (c *Client) huaweiSnapshotState(ctx context.Context, region string, target provider.SnapshotTarget) (snapshotState, error) {
	endpoint := huaweiEVS(region, c.huaweiSnapshotCollection(target.Class)+"/detail")
	endpoint.RawQuery = url.Values{"id": {target.ID}, "limit": {"2"}}.Encode()
	var result struct {
		Count         *int
		SnapshotLinks []struct{ Rel string } `json:"snapshots_links"`
		Snapshots     []struct {
			ID, Status, Name, Category string
			VolumeID                   string `json:"volume_id"`
			Project                    string `json:"project_id"`
			LegacyProject              string `json:"os-extended-snapshot-attributes:project_id"`
			SnapshotGroupID            string `json:"snapshot_group_id"`
			SnapshotType               string `json:"snapshot_type"`
			Size                       json.Number
			Zone                       string `json:"availability_zone"`
			Encrypted                  *bool
			Metadata                   map[string]string
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return snapshotState{}, err
	}
	rows := result.Snapshots
	if rows == nil || len(rows) > 1 || (result.Count != nil && *result.Count != len(rows)) ||
		slices.ContainsFunc(result.SnapshotLinks, func(link struct{ Rel string }) bool { return link.Rel == "next" }) {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return snapshotState{}, nil
	}
	row := rows[0]
	if (target.Class == "flash" && row.LegacyProject != c.profile.ProjectID) ||
		(row.Project != "" && row.Project != c.profile.ProjectID) || (row.LegacyProject != "" && row.LegacyProject != c.profile.ProjectID) {
		return snapshotState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if row.ID != target.ID || row.VolumeID != target.VolumeID || (row.Category != "" && row.Category != target.Class) ||
		(target.Class == "standard" && row.Category != "standard") || row.Status == "" || row.SnapshotType == "copy" {
		return snapshotState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	size, err := snapshotSize(row.Size)
	if err != nil {
		return snapshotState{}, err
	}
	encrypted := row.Encrypted
	if target.Class == "flash" && encrypted == nil && row.Metadata != nil {
		flag := row.Metadata["__system__encrypted"]
		if flag == "" || flag == "0" || flag == "1" {
			value := flag == "1"
			encrypted = &value
		}
	}
	name := strings.ToLower(row.Name)
	ready := row.Status == "available" && name != "" && !strings.HasPrefix(name, "autobk_snapshot_") &&
		!strings.HasPrefix(name, "manualbk_snapshot_")
	return snapshotState{present: true, ready: ready, size: size, zone: row.Zone, encrypted: encrypted,
		deletable: ready && row.SnapshotGroupID == "" && (target.Class == "flash" || row.SnapshotType == "user"),
	}, nil
}

func (c *Client) deleteSnapshot(ctx context.Context, region string, target provider.SnapshotTarget) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct {
			SnapshotIds      []string
			DeleteBindImages bool
		}{[]string{target.ID}, false})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "cbs", region, "DeleteSnapshots", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		query := url.Values{"RegionId": {region}, "SnapshotId": {target.ID}, "Force": {"false"}}
		var result struct{ Code, RequestId string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DeleteSnapshot", "2014-05-26", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		endpoint := huaweiEVS(region, c.huaweiSnapshotCollection(target.Class)+"/"+target.ID)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusAccepted, http.StatusNoContent)
	}
}
