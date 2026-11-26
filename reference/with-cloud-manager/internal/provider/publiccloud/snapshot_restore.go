package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"with-cloud-manager/internal/provider"
)

const restoreIntentTag = "plumego-cmp-restore"

func (c *Client) checkSnapshotRestoreScope(scope provider.Scope, spec provider.SnapshotRestoreSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Volume || !slices.Contains(c.profile.Regions, scope.Region) ||
		!launchTokenPattern.MatchString(token) || !spec.ValidFor(c.name, scope.Region) {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func restoreMarker(scope provider.Scope, token string) string {
	return sha([]byte(restoreIntentTag + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func (c *Client) PrepareSnapshotRestore(ctx context.Context, scope provider.Scope, spec provider.SnapshotRestoreSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkSnapshotRestoreScope(scope, spec, token); err != nil {
		return nil, err
	}
	if spec.SizeGiB > c.profile.SnapshotRestoreMaxGiB {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	state, err := c.readSnapshotState(ctx, scope.Region, spec.SnapshotTarget)
	if err != nil {
		return nil, err
	}
	if !state.ready || state.size <= 0 || state.size > int64(spec.SizeGiB) ||
		state.encrypted == nil || *state.encrypted != spec.Encrypted ||
		(c.name == provider.HuaweiECS && state.zone != "" && state.zone != spec.Zone) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	marker := restoreMarker(scope, token)
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.restoreTencentVolume(ctx, scope.Region, spec, token, marker)
		case provider.AlibabaECS:
			return c.restoreAlibabaVolume(ctx, scope.Region, spec, token, marker)
		default:
			return c.restoreHuaweiVolume(ctx, scope.Region, spec, marker)
		}
	}, nil
}

func (c *Client) restoreTencentVolume(ctx context.Context, region string, spec provider.SnapshotRestoreSpec, token, marker string) (provider.LaunchReceipt, error) {
	input := struct {
		Placement                      struct{ Zone string }
		SnapshotId, DiskName, DiskType string
		DiskChargeType, ClientToken    string
		DiskSize, DiskCount            int
		Tags                           []tag
		Encrypt                        string `json:",omitempty"`
	}{SnapshotId: spec.ID, DiskName: spec.Name, DiskType: spec.DiskType, DiskSize: spec.SizeGiB, DiskCount: 1,
		DiskChargeType: "POSTPAID_BY_HOUR", ClientToken: token, Tags: []tag{{Key: restoreIntentTag, Value: marker}}}
	input.Placement.Zone = spec.Zone
	if spec.Encrypted {
		input.Encrypt = "ENCRYPT"
	}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		RequestId string
		DiskIdSet []string
	}
	if err := c.tencent(ctx, "cbs", region, "CreateDisks", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt(result.DiskIdSet, result.RequestId, "disk-")
}

func (c *Client) restoreAlibabaVolume(ctx context.Context, region string, spec provider.SnapshotRestoreSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "ZoneId": {spec.Zone}, "SnapshotId": {spec.ID}, "DiskName": {spec.Name},
		"DiskCategory": {spec.DiskType}, "Size": {strconv.Itoa(spec.SizeGiB)}, "DiskChargeType": {"PostPaid"}, "ClientToken": {token},
		"Tag.1.Key": {restoreIntentTag}, "Tag.1.Value": {marker}}
	if spec.Encrypted {
		query.Set("Encrypted", "true")
	}
	var result struct{ RequestId, Code, DiskId string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "CreateDisk", "2014-05-26", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return creationReceipt([]string{result.DiskId}, result.RequestId, "d-")
}

func (c *Client) restoreHuaweiVolume(ctx context.Context, region string, spec provider.SnapshotRestoreSpec, marker string) (provider.LaunchReceipt, error) {
	input := struct {
		Volume struct {
			SnapshotID  string `json:"snapshot_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Zone        string `json:"availability_zone"`
			Type        string `json:"volume_type"`
			Size        int    `json:"size"`
			Count       int    `json:"count"`
		} `json:"volume"`
	}{}
	input.Volume.SnapshotID, input.Volume.Name, input.Volume.Description = spec.ID, spec.Name, restoreIntentTag+":"+marker
	input.Volume.Zone, input.Volume.Type, input.Volume.Size, input.Volume.Count = spec.Zone, spec.DiskType, spec.SizeGiB, 1
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := huaweiEVS(region, "/v2.1/"+c.profile.ProjectID+"/cloudvolumes")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	// EVS 要求 36 字符的 UUID 格式标识。
	req.Header.Set("X-Client-Token", huaweiLaunchToken(marker))
	var result struct {
		JobID     string   `json:"job_id"`
		VolumeIDs []string `json:"volume_ids"`
	}
	if err := c.request(req, &result, http.StatusAccepted); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if len(result.VolumeIDs) != 1 || !huaweiResourceID.MatchString(result.VolumeIDs[0]) {
		return provider.LaunchReceipt{RequestID: result.JobID}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.LaunchReceipt{RequestID: result.JobID, ResourceID: result.VolumeIDs[0]}, nil
}
