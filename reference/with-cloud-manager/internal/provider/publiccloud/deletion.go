package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareInstanceDeletion(ctx context.Context, scope provider.Scope, id string) (func(context.Context) (string, error), error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return nil, err
	}
	if c.name != provider.AlibabaECS && c.name != provider.HuaweiECS {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if !c.profile.AllowInstanceDelete {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	if c.name == provider.AlibabaECS {
		if err := c.checkAlibabaDeletion(ctx, scope.Region, id); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (string, error) {
			return c.deleteAlibabaInstance(ctx, scope.Region, id)
		}, nil
	}
	if err := c.checkHuaweiDeletion(ctx, scope.Region, id); err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		return c.deleteHuaweiInstance(ctx, scope.Region, id)
	}, nil
}

func (c *Client) checkAlibabaDeletion(ctx context.Context, region, id string) error {
	instance, err := c.describeAlibabaInstance(ctx, region, id)
	if err != nil {
		return err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if instance.Status != "Stopped" || instance.InstanceChargeType != "PostPaid" || instance.ZoneId == "" ||
		instance.OperationLocks.LockReason == nil || len(instance.OperationLocks.LockReason) != 0 {
		return blocked
	}
	disks, err := c.describeAlibabaDisks(ctx, region, url.Values{"InstanceId": {id}})
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(disks))
	roots := 0
	for _, disk := range disks {
		if _, exists := seen[disk.DiskId]; exists {
			return blocked
		}
		seen[disk.DiskId] = struct{}{}
		hosts := disk.hosts()
		if !strings.HasPrefix(disk.DiskId, "d-") || !resourceID.MatchString(disk.DiskId) ||
			disk.RegionId != region || disk.ZoneId != instance.ZoneId || disk.Status != "In_use" ||
			!disk.cloudDisk() || !disk.retained() || disk.MultiAttach != "Disabled" ||
			len(hosts) != 1 || hosts[0] != id {
			return blocked
		}
		switch disk.Type {
		case "system":
			roots++
		case "data":
		default:
			return blocked
		}
	}
	if roots != 1 {
		return blocked
	}
	return nil
}

func (c *Client) deleteAlibabaInstance(ctx context.Context, region, id string) (string, error) {
	query := url.Values{"InstanceId": {id}, "Force": {"false"}, "TerminateSubscription": {"false"}}
	var response struct{ RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DeleteInstance", "2014-05-26", query, &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) checkHuaweiDeletion(ctx context.Context, region, id string) error {
	instance, err := c.describeHuaweiInstance(ctx, region, id)
	if err != nil {
		return err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if instance.Status != "SHUTOFF" || instance.Locked == nil || *instance.Locked || instance.Volumes == nil {
		return blocked
	}
	devices := make(map[string]string, len(instance.Volumes))
	for _, disk := range instance.Volumes {
		if !huaweiResourceID.MatchString(disk.ID) || disk.BootIndex != "-1" || disk.Device == "" ||
			disk.Device == instance.RootDevice || !huaweiRetainsVolume(disk.DeleteOnTermination) || devices[disk.ID] != "" {
			return blocked
		}
		devices[disk.ID] = disk.Device
	}
	return c.checkHuaweiVolumeAttachments(ctx, region, id, devices)
}

func (c *Client) checkHuaweiVolumeAttachments(ctx context.Context, region, id string, devices map[string]string) error {
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + id + "/os-volume_attachments"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var response struct {
		VolumeAttachments []struct{ ID, ServerID, VolumeID, Device string }
	}
	if err := c.request(req, &response); err != nil {
		return err
	}
	if response.VolumeAttachments == nil || len(response.VolumeAttachments) != len(devices) {
		return blocked
	}
	for _, attachment := range response.VolumeAttachments {
		if attachment.ServerID != id || attachment.ID != attachment.VolumeID ||
			attachment.Device == "" || devices[attachment.VolumeID] != attachment.Device {
			return blocked
		}
		delete(devices, attachment.VolumeID)
	}
	return nil
}

func (c *Client) deleteHuaweiInstance(ctx context.Context, region, id string) (string, error) {
	type server struct {
		ID string `json:"id"`
	}
	payload := struct {
		Servers        []server `json:"servers"`
		DeleteVolume   bool     `json:"delete_volume"`
		DeletePublicIP bool     `json:"delete_publicip"`
	}{Servers: []server{{ID: id}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/delete"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var response struct {
		JobID string `json:"job_id"`
	}
	if err := c.request(req, &response, http.StatusAccepted); err != nil {
		return "", err
	}
	return operationReceipt(response.JobID)
}

type huaweiOperator struct{ rebootObserver }

func (c huaweiOperator) ObserveDeletion(ctx context.Context, scope provider.Scope, id, receipt string) (provider.ActionResult, error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return "", err
	}
	if !resourceID.MatchString(receipt) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return c.observeHuaweiJob(ctx, scope.Region, id, receipt, "deleteVMs", "deleteVM")
}
