package publiccloud

import (
	"context"
	"encoding/json"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareInstanceRetirement(ctx context.Context, scope provider.Scope, target provider.InstanceRetirement) (func(context.Context) (string, error), error) {
	if err := c.checkInstanceScope(scope, target.InstanceID); err != nil {
		return nil, err
	}
	if !target.ValidFor(c.name) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if !c.profile.AllowInstanceRetirement {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	row, err := c.readTencentInstance(ctx, scope.Region, target.InstanceID)
	if err != nil {
		return nil, err
	}
	if row == nil || row.InstanceState != "STOPPED" || row.InstanceChargeType != "POSTPAID_BY_HOUR" ||
		row.LatestOperationState == "OPERATING" ||
		row.DisableApiTermination == nil || *row.DisableApiTermination || row.Placement.Zone == "" ||
		row.SystemDisk.DiskId != target.SystemDiskID || row.DataDisks == nil || len(row.DataDisks) != 0 {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	if err := c.checkTencentRetirementDisks(ctx, scope.Region, row.Placement.Zone, target); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		body, err := json.Marshal(struct {
			InstanceIds             []string
			ReleasePrepaidDataDisks bool
		}{InstanceIds: []string{target.InstanceID}})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "cvm", scope.Region, "TerminateInstances", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	}, nil
}

func (c *Client) checkTencentRetirementDisks(ctx context.Context, region, zone string, target provider.InstanceRetirement) error {
	body, err := json.Marshal(struct {
		Filters []struct {
			Name   string
			Values []string
		}
		Limit int
	}{Filters: []struct {
		Name   string
		Values []string
	}{{Name: "instance-id", Values: []string{target.InstanceID}}}, Limit: 2})
	if err != nil {
		return err
	}
	var result struct {
		TotalCount *int
		DiskSet    []struct {
			DiskId, DiskState, DiskUsage, InstanceId string
			InstanceIdList                           []string
			Placement                                struct{ Zone string }
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &result); err != nil {
		return err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if result.TotalCount == nil || *result.TotalCount != 1 || len(result.DiskSet) != 1 {
		return blocked
	}
	disk := result.DiskSet[0]
	hosts := distinctVolumeHosts(append(disk.InstanceIdList, disk.InstanceId))
	if disk.DiskId != target.SystemDiskID || disk.DiskUsage != "SYSTEM_DISK" || disk.DiskState != "ATTACHED" ||
		disk.Placement.Zone != zone || len(hosts) != 1 || hosts[0] != target.InstanceID {
		return blocked
	}
	return nil
}

func (c *Client) ObserveInstanceRetirement(ctx context.Context, scope provider.Scope, id string) (provider.ActionResult, error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return "", err
	}
	if c.name != provider.TencentCVM {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	row, err := c.readTencentInstance(ctx, scope.Region, id)
	if err != nil {
		return "", err
	}
	if row != nil {
		return provider.ActionPending, nil
	}
	body, err := json.Marshal(struct{ InstanceIds []string }{[]string{id}})
	if err != nil {
		return "", err
	}
	var result struct {
		TotalCount        *int
		InstanceStatusSet []struct{ InstanceId, InstanceState string }
	}
	if err := c.tencent(ctx, "cvm", scope.Region, "DescribeInstancesStatus", "2017-03-12", body, &result); err != nil {
		return "", err
	}
	if result.TotalCount == nil || result.InstanceStatusSet == nil ||
		*result.TotalCount != len(result.InstanceStatusSet) || len(result.InstanceStatusSet) > 1 {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.InstanceStatusSet) == 0 {
		return provider.ActionSucceeded, nil
	}
	if result.InstanceStatusSet[0].InstanceId != id || result.InstanceStatusSet[0].InstanceState == "" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.ActionPending, nil
}
