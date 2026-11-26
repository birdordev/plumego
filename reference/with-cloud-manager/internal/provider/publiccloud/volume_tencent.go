package publiccloud

import (
	"context"
	"encoding/json"
	"slices"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentVolumeEvidence(ctx context.Context, region string, change provider.VolumeChange) (volumeEvidence, error) {
	instance, err := c.describeTencentInstance(ctx, region, change.InstanceID)
	if err != nil {
		return volumeEvidence{}, err
	}
	body, err := json.Marshal(struct{ DiskIds []string }{[]string{change.VolumeID}})
	if err != nil {
		return volumeEvidence{}, err
	}
	var response struct {
		TotalCount *int
		DiskSet    []struct {
			DiskId, DiskState, DiskUsage, InstanceId string
			Placement                                struct{ Zone string }
			InstanceIdList                           []string
			Portable, Shareable, DeleteWithInstance  *bool
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &response); err != nil {
		return volumeEvidence{}, err
	}
	if response.DiskSet == nil || response.TotalCount == nil || *response.TotalCount != len(response.DiskSet) || len(response.DiskSet) > 1 {
		return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(response.DiskSet) == 0 {
		return volumeEvidence{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	disk := response.DiskSet[0]
	if disk.DiskId != change.VolumeID {
		return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	retained := disk.DeleteWithInstance != nil && !*disk.DeleteWithInstance
	return volumeEvidence{
		instanceZone: instance.Placement.Zone, volumeZone: disk.Placement.Zone,
		available: disk.DiskState == "UNATTACHED", attached: disk.DiskState == "ATTACHED", retained: retained,
		safe: disk.DiskUsage == "DATA_DISK" && disk.Portable != nil && *disk.Portable &&
			disk.Shareable != nil && !*disk.Shareable && retained && instance.SystemDisk.DiskId != change.VolumeID,
		instanceHasVolume: slices.ContainsFunc(instance.DataDisks, func(item struct{ DiskId string }) bool {
			return item.DiskId == change.VolumeID
		}),
		hosts: distinctVolumeHosts(append(disk.InstanceIdList, disk.InstanceId)),
	}, nil
}

func (c *Client) tencentVolumeChange(ctx context.Context, region string, change provider.VolumeChange) (string, error) {
	action := "DetachDisks"
	if change.Attach {
		action = "AttachDisks"
	}
	body, err := json.Marshal(struct {
		DiskIds    []string
		InstanceId string
	}{[]string{change.VolumeID}, change.InstanceID})
	if err != nil {
		return "", err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "cbs", region, action, "2017-03-12", body, &response); err != nil {
		return "", err
	}
	return operationReceipt(response.RequestId)
}
