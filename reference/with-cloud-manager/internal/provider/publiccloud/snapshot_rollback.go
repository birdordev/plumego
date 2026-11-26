package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareSnapshotRollback(ctx context.Context, scope provider.Scope, target provider.SnapshotTarget) (func(context.Context) (string, error), error) {
	if err := c.checkSnapshotTargetScope(scope, target); err != nil {
		return nil, err
	}
	if !c.profile.AllowSnapshotRollback || !target.ValidForRollback(c.name) {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	snapshot, err := c.readSnapshotState(ctx, scope.Region, target)
	if err != nil {
		return nil, err
	}
	if !snapshot.ready {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	ready, err := c.rollbackVolumeAvailable(ctx, scope.Region, target.VolumeID, snapshot.size)
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.rollbackSnapshot(ctx, scope.Region, target)
	}, nil
}

func (c *Client) rollbackVolumeAvailable(ctx context.Context, region, id string, snapshotSize int64) (bool, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct{ DiskIds []string }{[]string{id}})
		if err != nil {
			return false, err
		}
		var result struct {
			TotalCount *int
			DiskSet    []struct {
				DiskId, DiskState, DiskUsage, InstanceId string
				InstanceIdList                           []string
				DiskSize                                 int64
			}
		}
		if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &result); err != nil {
			return false, err
		}
		if len(result.DiskSet) != 1 || result.DiskSet[0].DiskId != id || result.TotalCount == nil || *result.TotalCount != 1 {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		disk := result.DiskSet[0]
		return disk.DiskState == "UNATTACHED" && disk.DiskUsage == "DATA_DISK" && disk.InstanceId == "" &&
			len(disk.InstanceIdList) == 0 && snapshotSize > 0 && disk.DiskSize == snapshotSize, nil
	case provider.AlibabaECS:
		ids, err := json.Marshal([]string{id})
		if err != nil {
			return false, err
		}
		rows, err := c.describeAlibabaDisks(ctx, region, url.Values{"DiskIds": {string(ids)}})
		if err != nil {
			return false, err
		}
		if len(rows) != 1 || rows[0].DiskId != id {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		disk := rows[0]
		if disk.RegionId != region {
			return false, &provider.Failure{Kind: provider.Unauthorized}
		}
		return disk.Status == "Available" && disk.Type == "data" && disk.cloudDisk() && len(disk.hosts()) == 0, nil
	default:
		endpoint := huaweiEVS(region, "/v2/"+c.profile.ProjectID+"/cloudvolumes/"+id)
		var result struct {
			Volume struct {
				ID, Status, Bootable string
				Project              string `json:"os-vol-tenant-attr:tenant_id"`
				Attachments          []json.RawMessage
			}
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return false, err
		}
		disk := result.Volume
		if disk.Project != c.profile.ProjectID {
			return false, &provider.Failure{Kind: provider.Unauthorized}
		}
		if disk.ID != id || disk.Attachments == nil {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return disk.Status == "available" && disk.Bootable == "false" && len(disk.Attachments) == 0, nil
	}
}

func (c *Client) rollbackSnapshot(ctx context.Context, region string, target provider.SnapshotTarget) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct{ DiskId, SnapshotId string }{target.VolumeID, target.ID})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "cbs", region, "ApplySnapshot", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		query := url.Values{"DiskId": {target.VolumeID}, "SnapshotId": {target.ID}}
		var result struct{ Code, RequestId string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "ResetDisk", "2014-05-26", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		return c.rollbackHuaweiSnapshot(ctx, region, target)
	}
}

type rollbackVolume struct {
	VolumeID string `json:"volume_id"`
}

func (c *Client) rollbackHuaweiSnapshot(ctx context.Context, region string, target provider.SnapshotTarget) (string, error) {
	body, err := json.Marshal(struct {
		Rollback rollbackVolume `json:"rollback"`
	}{Rollback: rollbackVolume{VolumeID: target.VolumeID}})
	if err != nil {
		return "", err
	}
	endpoint := huaweiEVS(region, c.huaweiSnapshotCollection(target.Class)+"/"+target.ID+"/rollback")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var result struct {
		Rollback rollbackVolume
		JobID    string `json:"job_id"`
	}
	if err := c.request(req, &result, http.StatusAccepted); err != nil {
		return "", err
	}
	if target.Class == "flash" && result.Rollback.VolumeID == target.VolumeID {
		return "", nil
	}
	if target.Class != "standard" || !resourceID.MatchString(result.JobID) ||
		(result.Rollback.VolumeID != "" && result.Rollback.VolumeID != target.VolumeID) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.JobID, nil
}

func (c *Client) ObserveSnapshotRollback(ctx context.Context, scope provider.Scope, target provider.SnapshotTarget, jobID string) (provider.ActionResult, error) {
	if err := c.checkSnapshotTargetScope(scope, target); err != nil {
		return provider.ActionPending, err
	}
	if c.name != provider.HuaweiECS || target.Class != "standard" || !resourceID.MatchString(jobID) {
		return provider.ActionPending, &provider.Failure{Kind: provider.Unsupported}
	}
	endpoint := huaweiEVS(scope.Region, "/v1/"+c.profile.ProjectID+"/jobs/"+jobID)
	var result struct {
		JobID    string `json:"job_id"`
		Status   string
		Entities struct {
			VolumeID string            `json:"volume_id"`
			SubJobs  []json.RawMessage `json:"sub_jobs"`
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.ActionPending, err
	}
	if result.JobID != jobID || (result.Entities.VolumeID != "" && result.Entities.VolumeID != target.VolumeID) || len(result.Entities.SubJobs) != 0 {
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch result.Status {
	case "SUCCESS", "FAIL":
		if result.Entities.VolumeID != target.VolumeID {
			return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if result.Status == "SUCCESS" {
			return provider.ActionSucceeded, nil
		}
		return provider.ActionFailed, nil
	case "RUNNING", "INIT", "WAITING_EXECUTE", "LISTENING":
		return provider.ActionPending, nil
	default:
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
}
