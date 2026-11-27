package publiccloud

import (
	"context"

	"with-cloud-manager/internal/provider"
)

type detachedVolumeEvidence struct {
	canRelease bool
	size       *int
	available  bool
	safe       bool
}

func (c *Client) inspectDetachedVolume(ctx context.Context, region, id string) (detachedVolumeEvidence, error) {
	switch c.name {
	case provider.TencentCVM:
		disk, err := c.describeTencentDisk(ctx, region, id)
		return detachedVolumeEvidence{
			canRelease: true, size: disk.DiskSize, available: disk.DiskState == "UNATTACHED",
			safe: disk.DiskUsage == "DATA_DISK" && disk.DiskChargeType == "POSTPAID_BY_HOUR" &&
				disk.Attached != nil && !*disk.Attached && disk.Portable != nil && *disk.Portable &&
				disk.Shareable != nil && !*disk.Shareable && len(distinctVolumeHosts(append(disk.InstanceIdList, disk.InstanceId))) == 0,
		}, err
	case provider.AlibabaECS:
		disk, err := c.describeAlibabaDisk(ctx, region, id)
		return detachedVolumeEvidence{
			canRelease: disk.DeleteAutoSnapshot != nil && !*disk.DeleteAutoSnapshot,
			size:       disk.Size, available: disk.Status == "Available",
			safe: disk.Type == "data" && disk.DiskChargeType == "PostPaid" && disk.cloudDisk() &&
				disk.Portable != nil && *disk.Portable && disk.MultiAttach == "Disabled" && len(disk.hosts()) == 0,
		}, err
	default:
		disk, err := c.describeHuaweiDisk(ctx, region, id)
		order := disk.Metadata["orderID"]
		return detachedVolumeEvidence{
			canRelease: true, size: disk.Size, available: disk.Status == "available",
			safe: disk.Bootable == "false" && disk.Multiattach != nil && !*disk.Multiattach &&
				disk.Attachments != nil && len(disk.Attachments) == 0 && disk.Metadata != nil &&
				(len(order) == 0 || string(order) == `""`),
		}, err
	}
}
