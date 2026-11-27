package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkAlibabaRetirement(ctx context.Context, region string, target provider.InstanceRetirement) error {
	row, err := c.readAlibabaInstance(ctx, region, target.InstanceID)
	if err != nil {
		return err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if row == nil || row.Status != "Stopped" || row.InstanceChargeType != "PostPaid" || row.ZoneId == "" ||
		row.OperationLocks.LockReason == nil || len(row.OperationLocks.LockReason) != 0 ||
		row.DeletionProtection == nil || *row.DeletionProtection {
		return blocked
	}
	disks, err := c.describeAlibabaDisks(ctx, region, url.Values{"InstanceId": {target.InstanceID}})
	if err != nil {
		return err
	}
	if len(disks) != 1 {
		return blocked
	}
	disk := disks[0]
	hosts := disk.hosts()
	if disk.DiskId != target.SystemDiskID || disk.RegionId != region || disk.ZoneId != row.ZoneId ||
		disk.Type != "system" || disk.Status != "In_use" || !disk.cloudDisk() || disk.MultiAttach != "Disabled" ||
		disk.DeleteWithInstance == nil || !*disk.DeleteWithInstance || disk.DeleteAutoSnapshot == nil || *disk.DeleteAutoSnapshot ||
		len(hosts) != 1 || hosts[0] != target.InstanceID {
		return blocked
	}
	return nil
}

func (c *Client) observeAlibabaRetirement(ctx context.Context, region string, target provider.InstanceRetirement) (provider.ActionResult, error) {
	row, err := c.readAlibabaInstance(ctx, region, target.InstanceID)
	if err != nil {
		return "", err
	}
	if row != nil {
		return provider.ActionPending, nil
	}
	ids, err := json.Marshal([]string{target.SystemDiskID})
	if err != nil {
		return "", err
	}
	disks, err := c.describeAlibabaDisks(ctx, region, url.Values{"DiskIds": {string(ids)}})
	if err != nil {
		return "", err
	}
	if len(disks) == 0 {
		return provider.ActionSucceeded, nil
	}
	if len(disks) != 1 || disks[0].DiskId != target.SystemDiskID || disks[0].RegionId != region || disks[0].Status == "" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.ActionPending, nil
}
