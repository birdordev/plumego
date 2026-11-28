package publiccloud

import (
	"context"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const tencentResizeInstanceAction = "ResetInstancesType"

func (c *Client) PrepareInstanceResize(ctx context.Context, scope provider.Scope, change provider.InstanceResize) (func(context.Context) (string, error), error) {
	if err := c.checkInstanceScope(scope, change.ID); err != nil {
		return nil, err
	}
	if !change.ValidFor(c.name) || !slices.Contains(c.profile.InstanceResizeSizes, change.TargetType) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	options, err := c.InstanceResizeOptions(ctx, scope, change.ID, change.CurrentType)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(options.Types, change.TargetType) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.resizeTencentInstance(ctx, scope.Region, change)
		case provider.AlibabaECS:
			return c.resizeAlibabaInstance(ctx, scope.Region, change)
		default:
			return c.resizeHuaweiInstance(ctx, scope.Region, change)
		}
	}, nil
}

func (c *Client) checkInstanceResize(ctx context.Context, region, id, current string) (string, error) {
	blocked := &provider.Failure{Kind: provider.PreconditionFailed}
	stopped := provider.InstanceStoppedState(c.name)
	switch c.name {
	case provider.TencentCVM:
		row, err := c.describeTencentInstance(ctx, region, id)
		if err != nil {
			return "", err
		}
		if row.InstanceState != stopped || row.InstanceType != current ||
			row.InstanceChargeType != "POSTPAID_BY_HOUR" || row.Placement.Zone == "" || row.LatestOperationState == "OPERATING" ||
			!slices.Contains([]string{"CLOUD_BASIC", "CLOUD_PREMIUM", "CLOUD_SSD", "CLOUD_BSSD"}, row.SystemDisk.DiskType) {
			return "", blocked
		}
		for _, disk := range row.DataDisks {
			if !strings.HasPrefix(disk.DiskType, "CLOUD_") {
				return "", blocked
			}
		}
		return row.Placement.Zone, nil
	case provider.AlibabaECS:
		row, err := c.describeAlibabaInstance(ctx, region, id)
		if err != nil {
			return "", err
		}
		if row.Status != stopped || row.InstanceType != current ||
			row.InstanceChargeType != "PostPaid" || row.SpotStrategy != "NoSpot" ||
			row.StoppedMode != "KeepCharging" || row.ZoneId == "" ||
			row.OperationLocks.LockReason == nil || len(row.OperationLocks.LockReason) != 0 {
			return "", blocked
		}
		return row.ZoneId, nil
	case provider.HuaweiECS:
		row, err := c.describeHuaweiInstance(ctx, region, id)
		if err != nil {
			return "", err
		}
		if row.Status != stopped || row.Flavor.ID != current ||
			row.Metadata.ChargingMode != "0" || row.Locked == nil || *row.Locked || row.TaskState != "" || row.Zone == "" {
			return "", blocked
		}
		return row.Zone, nil
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) ObserveInstanceResize(ctx context.Context, scope provider.Scope, change provider.InstanceResize, receipt string) (provider.ActionResult, string, error) {
	if err := c.checkInstanceScope(scope, change.ID); err != nil {
		return "", "", err
	}
	if !change.ValidFor(c.name) || (receipt != "" && !resourceID.MatchString(receipt)) {
		return "", "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if c.name == provider.TencentCVM {
		return c.observeTencentInstanceResize(ctx, scope.Region, change, receipt)
	}
	stopped := provider.InstanceStoppedState(c.name)
	if c.name == provider.AlibabaECS {
		row, err := c.describeAlibabaInstance(ctx, scope.Region, change.ID)
		if err != nil {
			return "", "", err
		}
		if row.InstanceType == change.TargetType && row.Status == stopped &&
			row.StoppedMode == "KeepCharging" && row.InstanceChargeType == "PostPaid" && row.SpotStrategy == "NoSpot" {
			return provider.ActionSucceeded, row.Status, nil
		}
		return provider.ActionPending, "", nil
	}
	if receipt != "" {
		result, err := c.observeHuaweiJob(ctx, scope.Region, change.ID, receipt, "", "resizeServer")
		if err != nil || result != provider.ActionSucceeded {
			return result, "", err
		}
	}
	row, err := c.describeHuaweiInstance(ctx, scope.Region, change.ID)
	if err != nil {
		return "", "", err
	}
	if row.Flavor.ID == change.TargetType && row.TaskState == "" && row.Status == stopped && row.Metadata.ChargingMode == "0" {
		return provider.ActionSucceeded, row.Status, nil
	}
	return provider.ActionPending, "", nil
}

func (c *Client) observeTencentInstanceResize(ctx context.Context, region string, change provider.InstanceResize, receipt string) (provider.ActionResult, string, error) {
	row, err := c.describeTencentInstance(ctx, region, change.ID)
	if err != nil {
		return "", "", err
	}
	if !resourceID.MatchString(row.LatestOperationRequestId) || (receipt != "" && row.LatestOperationRequestId != receipt) {
		return provider.ActionPending, "", nil
	}
	if row.LatestOperation != tencentResizeInstanceAction {
		if receipt == "" {
			return provider.ActionPending, "", nil
		}
		return "", "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch row.LatestOperationState {
	case "OPERATING":
		return provider.ActionPending, "", nil
	case "FAILED":
		if receipt == "" {
			return provider.ActionPending, "", nil
		}
		return provider.ActionFailed, "", nil
	case "SUCCESS":
		if row.InstanceType == change.TargetType && row.InstanceState == "RUNNING" && row.InstanceChargeType == "POSTPAID_BY_HOUR" {
			return provider.ActionSucceeded, row.InstanceState, nil
		}
		return provider.ActionPending, "", nil
	default:
		return "", "", &provider.Failure{Kind: provider.InvalidResponse}
	}
}
