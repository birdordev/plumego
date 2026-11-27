package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkVolumeReleaseScope(scope provider.Scope, target provider.VolumeRelease) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Volume || !slices.Contains(c.profile.Regions, scope.Region) || !target.ValidFor(c.name) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) PrepareVolumeRelease(ctx context.Context, scope provider.Scope, target provider.VolumeRelease) (func(context.Context) (string, error), error) {
	if err := c.checkVolumeReleaseScope(scope, target); err != nil {
		return nil, err
	}
	if !c.profile.AllowVolumeRelease {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	// 先查快照，在尽可能靠近派发的位置重新读取云盘状态。
	if err := c.checkVolumeSnapshotDependencies(ctx, scope.Region, target.ID); err != nil {
		return nil, err
	}
	evidence, err := c.inspectDetachedVolume(ctx, scope.Region, target.ID)
	if err != nil {
		return nil, err
	}
	if !evidence.safe || !evidence.available || !evidence.canRelease || evidence.size == nil || *evidence.size != target.SizeGiB {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.releaseVolume(ctx, scope.Region, target.ID)
	}, nil
}

func (c *Client) ObserveVolumeRelease(ctx context.Context, scope provider.Scope, target provider.VolumeRelease, receipt string) (provider.ActionResult, error) {
	if err := c.checkVolumeReleaseScope(scope, target); err != nil {
		return provider.ActionPending, err
	}
	if c.name == provider.HuaweiECS && receipt != "" {
		result, err := c.observeHuaweiVolumeJob(ctx, scope.Region, receipt, "deleteVolume", target.ID, 0)
		if err != nil || result != provider.ActionSucceeded {
			return result, err
		}
	}
	absent, err := c.volumeAbsentFromInventory(ctx, scope.Region, target.ID)
	if err != nil || !absent {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) volumeAbsentFromInventory(ctx context.Context, region, id string) (bool, error) {
	switch c.name {
	case provider.TencentCVM:
		disk, err := c.findTencentDisk(ctx, region, id)
		return disk.DiskId == "", err
	case provider.AlibabaECS:
		disk, err := c.findAlibabaDisk(ctx, region, id)
		return disk.DiskId == "", err
	default:
		endpoint := huaweiEVS(region, "/v2/"+c.profile.ProjectID+"/cloudvolumes/detail")
		endpoint.RawQuery = url.Values{"id": {id}, "enterprise_project_id": {"all_granted_eps"}, "limit": {"2"}}.Encode()
		var result struct {
			Count   *int
			Volumes []struct {
				ID      string
				Project string `json:"os-vol-tenant-attr:tenant_id"`
			}
			Links []struct{ Rel string } `json:"volumes_links"`
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return false, err
		}
		if result.Volumes == nil || result.Count == nil || *result.Count != len(result.Volumes) || len(result.Volumes) > 1 ||
			slices.ContainsFunc(result.Links, func(link struct{ Rel string }) bool { return link.Rel == "next" }) {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if len(result.Volumes) == 1 && (result.Volumes[0].ID != id || result.Volumes[0].Project != c.profile.ProjectID) {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return len(result.Volumes) == 0, nil
	}
}

func (c *Client) releaseVolume(ctx context.Context, region, id string) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct {
			DiskIds        []string
			DeleteSnapshot int
		}{[]string{id}, 0})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "cbs", region, "TerminateDisks", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		var result struct{ Code, RequestId string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DeleteDisk", "2014-05-26", url.Values{"DiskId": {id}}, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		endpoint := huaweiEVS(region, "/v2/"+c.profile.ProjectID+"/cloudvolumes/"+id)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		var result struct {
			JobID string `json:"job_id"`
		}
		if err := c.request(req, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.JobID)
	}
}
