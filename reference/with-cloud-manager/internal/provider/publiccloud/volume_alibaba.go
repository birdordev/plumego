package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaDiskObservation struct {
	DiskId, RegionId, ZoneId, InstanceId, Type, Status, Category, MultiAttach string
	Portable, DeleteWithInstance, DeleteAutoSnapshot                          *bool
	Attachments                                                               struct{ Attachment []struct{ InstanceId string } }
	MountInstances                                                            struct{ MountInstance []struct{ InstanceId string } }
}

func (disk alibabaDiskObservation) hosts() []string {
	hosts := []string{disk.InstanceId}
	for _, attachment := range disk.Attachments.Attachment {
		hosts = append(hosts, attachment.InstanceId)
	}
	for _, mount := range disk.MountInstances.MountInstance {
		hosts = append(hosts, mount.InstanceId)
	}
	return distinctVolumeHosts(hosts)
}

func (disk alibabaDiskObservation) retained() bool {
	return disk.DeleteWithInstance != nil && !*disk.DeleteWithInstance
}

func (disk alibabaDiskObservation) cloudDisk() bool {
	return disk.Category == "cloud" || strings.HasPrefix(disk.Category, "cloud_")
}

func (c *Client) describeAlibabaDisks(ctx context.Context, region string, query url.Values) ([]alibabaDiskObservation, error) {
	query.Set("RegionId", region)
	query.Set("MaxResults", strconv.Itoa(pageSize))
	var response struct {
		Code, NextToken string
		Disks           struct{ Disk []alibabaDiskObservation }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeDisks", "2014-05-26", query, &response); err != nil {
		return nil, err
	}
	if response.Code != "" {
		return nil, cloudFailure(response.Code)
	}
	if response.Disks.Disk == nil || response.NextToken != "" {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return response.Disks.Disk, nil
}

func (c *Client) alibabaVolumeEvidence(ctx context.Context, region string, change provider.VolumeChange) (volumeEvidence, error) {
	instance, err := c.describeAlibabaInstance(ctx, region, change.InstanceID)
	if err != nil {
		return volumeEvidence{}, err
	}
	ids, err := json.Marshal([]string{change.VolumeID})
	if err != nil {
		return volumeEvidence{}, err
	}
	rows, err := c.describeAlibabaDisks(ctx, region, url.Values{"DiskIds": {string(ids)}})
	if err != nil {
		return volumeEvidence{}, err
	}
	if len(rows) > 1 {
		return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return volumeEvidence{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	disk := rows[0]
	if disk.RegionId != region {
		return volumeEvidence{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if disk.DiskId != change.VolumeID {
		return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	hosts := disk.hosts()
	retained := disk.retained()
	return volumeEvidence{
		instanceZone: instance.ZoneId, volumeZone: disk.ZoneId,
		available: disk.Status == "Available", attached: disk.Status == "In_use", retained: retained,
		safe: disk.Type == "data" && disk.cloudDisk() &&
			disk.Portable != nil && *disk.Portable && disk.MultiAttach == "Disabled" && retained,
		instanceHasVolume: len(hosts) == 1 && hosts[0] == change.InstanceID, hosts: hosts,
	}, nil
}

func (c *Client) alibabaVolumeChange(ctx context.Context, region string, change provider.VolumeChange) (string, error) {
	action := "DetachDisk"
	query := url.Values{"InstanceId": {change.InstanceID}, "DiskId": {change.VolumeID}, "DeleteWithInstance": {"false"}}
	if change.Attach {
		action = "AttachDisk"
		query.Set("Bootable", "false")
	}
	var response struct{ RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", action, "2014-05-26", query, &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	return operationReceipt(response.RequestId)
}
