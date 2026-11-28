package publiccloud

import (
	"context"
	"strconv"

	"with-cloud-manager/internal/provider"
)

func (c *Client) observeTencentDatabaseRecovery(ctx context.Context, scope provider.Scope, spec provider.DatabaseRecovery, token string, receipt provider.LaunchReceipt) (provider.Object, error) {
	if !launchTokenPattern.MatchString(token) ||
		(receipt.RequestID != "" && !resourceID.MatchString(receipt.RequestID)) ||
		(receipt.ResourceID != "" && !provider.ValidDatabaseRecoveryInstanceID(c.name, receipt.ResourceID)) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if receipt.RequestID != "" {
		var result struct{ Status string }
		if err := c.tencentRestoreWindow(ctx, "cdb", scope.Region, "DescribeAsyncRequestInfo", "2017-03-20",
			struct{ AsyncRequestId string }{receipt.RequestID}, &result); err != nil {
			return provider.Object{}, err
		}
		switch result.Status {
		case "INITIAL", "RUNNING", "UNDEFINED":
			return provider.Object{}, nil
		case "SUCCESS":
		default:
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	target, err := c.readTencentRecoveryInstance(ctx, scope.Region, "", tencentRecoveryMarker(scope, token))
	if err != nil {
		return provider.Object{}, err
	}
	if target.InstanceId == spec.SourceID || (receipt.ResourceID != "" && target.InstanceId != receipt.ResourceID) ||
		target.Status == nil || target.TaskStatus == nil {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if *target.Status == 0 || (*target.Status == 1 && *target.TaskStatus == 6) {
		return provider.Object{}, nil
	}
	if !target.readyForRecovery(spec.Flavor) || target.InstanceName != spec.Name || target.UniqVpcId != spec.VPC ||
		target.UniqSubnetId != spec.Subnet || target.PayType == nil || *target.PayType != 1 ||
		target.DeployMode == nil || *target.DeployMode != 0 || target.InstanceNodes != 2 ||
		target.Cpu != spec.CPU || target.Memory != spec.MemoryMiB || target.Volume != spec.StorageGiB {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := c.checkTencentRecoveryZone(ctx, scope.Region, target.Zone, spec.Zone); err != nil {
		return provider.Object{}, err
	}
	stamp, err := c.tencentRecoveryTime(spec)
	if err != nil {
		return provider.Object{}, err
	}
	confirmed, err := c.checkTencentRecoveryHistory(ctx, scope.Region, spec.SourceID, target.InstanceId, stamp)
	if err != nil || !confirmed {
		return provider.Object{}, err
	}
	if err := c.checkTencentRecoveryEncryption(ctx, scope.Region, target.InstanceId); err != nil {
		return provider.Object{}, err
	}
	var security struct {
		Groups []struct{ SecurityGroupId string }
	}
	if err := c.tencentRestoreWindow(ctx, "cdb", scope.Region, "DescribeDBSecurityGroups", "2017-03-20",
		struct{ InstanceId string }{target.InstanceId}, &security); err != nil {
		return provider.Object{}, err
	}
	if len(security.Groups) != 1 || security.Groups[0].SecurityGroupId != spec.SecurityGroup {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return target.asset(scope.Region, target.InstanceId, target.InstanceName, provider.TencentCDB, "mysql", target.EngineVersion)
}

func (c *Client) checkTencentRecoveryZone(ctx context.Context, region, observed, expected string) error {
	if observed == expected {
		return nil
	}
	id, err := strconv.Atoi(observed)
	if err != nil || id <= 0 || strconv.Itoa(id) != observed {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	zones, err := c.readTencentZones(ctx, region)
	if err != nil {
		return err
	}
	matches := 0
	for _, zone := range zones {
		if zone.ZoneId == observed {
			if zone.Zone != expected {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
			matches++
		}
	}
	if matches != 1 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (c *Client) checkTencentRecoveryHistory(ctx context.Context, region, sourceID, targetID, stamp string) (bool, error) {
	const limit, maxRecords = 100, 5000
	total := -1
	var status string
	seenJobs := make(map[int64]struct{})
	for offset := 0; total < 0 || offset < total; offset += limit {
		input := struct {
			InstanceId    string
			Offset, Limit int
		}{sourceID, offset, limit}
		var result struct {
			TotalCount *int
			Items      []struct {
				SrcInstanceId, DstInstanceId, RollbackStrategy, RollbackTargetTime, TaskStatus string
				CloneJobId                                                                     int64
			}
		}
		if err := c.tencentRestoreWindow(ctx, "cdb", region, "DescribeCloneList", "2017-03-20", input, &result); err != nil {
			return false, err
		}
		if result.TotalCount == nil || *result.TotalCount < 0 || *result.TotalCount > maxRecords ||
			result.Items == nil || (total >= 0 && *result.TotalCount != total) ||
			len(result.Items) != min(limit, *result.TotalCount-offset) {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		total = *result.TotalCount
		for _, item := range result.Items {
			if _, duplicate := seenJobs[item.CloneJobId]; duplicate || item.CloneJobId <= 0 {
				return false, &provider.Failure{Kind: provider.InvalidResponse}
			}
			seenJobs[item.CloneJobId] = struct{}{}
			if item.DstInstanceId != targetID {
				continue
			}
			if status != "" || item.SrcInstanceId != sourceID || item.RollbackStrategy != "timepoint" ||
				item.RollbackTargetTime != stamp || item.TaskStatus == "" {
				return false, &provider.Failure{Kind: provider.InvalidResponse}
			}
			status = item.TaskStatus
		}
	}
	switch status {
	case "success":
		return true, nil
	case "", "initial", "running", "wait_complete":
		return false, nil
	default:
		return false, &provider.Failure{Kind: provider.InvalidResponse}
	}
}
