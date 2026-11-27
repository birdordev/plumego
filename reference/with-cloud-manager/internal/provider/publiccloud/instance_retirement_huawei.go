package publiccloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkHuaweiRetirement(ctx context.Context, region string, target provider.InstanceRetirement) error {
	row, err := c.describeHuaweiInstance(ctx, region, target.InstanceID)
	if err != nil {
		return err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if row.Status != "SHUTOFF" || row.Metadata.ChargingMode != "0" || row.Locked == nil || *row.Locked ||
		row.TaskState != "" || row.Zone == "" || row.RootDevice == "" || len(row.Volumes) != 1 {
		return blocked
	}
	root := row.Volumes[0]
	if root.ID != target.SystemDiskID || root.BootIndex != "0" || root.Device != row.RootDevice ||
		(string(root.DeleteOnTermination) != "true" && string(root.DeleteOnTermination) != `"true"`) {
		return blocked
	}
	if err := c.checkHuaweiVolumeAttachments(ctx, region, target.InstanceID, map[string]string{root.ID: root.Device}); err != nil {
		return err
	}
	disk, err := c.readHuaweiRetirementDisk(ctx, region, target.SystemDiskID)
	if err != nil {
		return err
	}
	if disk == nil || disk.Status != "in-use" || disk.Bootable != "true" || disk.Multiattach == nil || *disk.Multiattach ||
		disk.Zone != row.Zone || len(disk.Attachments) != 1 || disk.Attachments[0].ServerID != target.InstanceID ||
		disk.Attachments[0].VolumeID != target.SystemDiskID {
		return blocked
	}
	return nil
}

func (c *Client) observeHuaweiRetirement(ctx context.Context, region string, target provider.InstanceRetirement, receipt string) (provider.ActionResult, error) {
	result, err := c.observeHuaweiJob(ctx, region, target.InstanceID, receipt, "deleteVMs", "deleteVM")
	if err != nil || result != provider.ActionSucceeded {
		return result, err
	}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + target.InstanceID}
	var response struct {
		huaweiRetirementFault
		Server *huaweiInstanceObservation
	}
	missing, err := c.readHuaweiRetirementTarget(ctx, endpoint, &response, &response.huaweiRetirementFault, "Ecs.0114")
	if err != nil {
		return "", err
	}
	if !missing {
		if response.Server == nil || response.Server.ID != target.InstanceID || response.Server.Status == "" {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		if response.Server.Project != c.profile.ProjectID {
			return "", &provider.Failure{Kind: provider.Unauthorized}
		}
		return provider.ActionPending, nil
	}
	if response.Server != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	disk, err := c.readHuaweiRetirementDisk(ctx, region, target.SystemDiskID)
	if err != nil {
		return "", err
	}
	if disk != nil {
		return provider.ActionPending, nil
	}
	return provider.ActionSucceeded, nil
}

type huaweiRetirementDisk struct {
	ID, Status, Bootable string
	Multiattach          *bool
	Project              string `json:"os-vol-tenant-attr:tenant_id"`
	Zone                 string `json:"availability_zone"`
	Attachments          []struct {
		ServerID string `json:"server_id"`
		VolumeID string `json:"volume_id"`
	}
}

func (c *Client) readHuaweiRetirementDisk(ctx context.Context, region, id string) (*huaweiRetirementDisk, error) {
	endpoint := url.URL{Scheme: "https", Host: "evs." + region + ".myhuaweicloud.com",
		Path: "/v2/" + c.profile.ProjectID + "/cloudvolumes/" + id}
	var response struct {
		huaweiRetirementFault
		Volume *huaweiRetirementDisk
	}
	missing, err := c.readHuaweiRetirementTarget(ctx, endpoint, &response, &response.huaweiRetirementFault, "EVS.5404")
	if err != nil {
		return nil, err
	}
	if missing && response.Volume == nil {
		return nil, nil
	}
	if missing || response.Volume == nil || response.Volume.ID != id || response.Volume.Status == "" {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if response.Volume.Project != c.profile.ProjectID {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	return response.Volume, nil
}

type huaweiRetirementFault struct {
	ErrorCode string `json:"error_code"`
	Error     struct{ Code string }
}

func (c *Client) readHuaweiRetirementTarget(ctx context.Context, endpoint url.URL, dst any, fault *huaweiRetirementFault,
	missingCode string,
) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	status, err := c.requestStatus(req, dst, http.StatusNotFound)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		recognized := false
		for _, code := range []string{fault.ErrorCode, fault.Error.Code} {
			if code != "" && !strings.EqualFold(code, missingCode) {
				return false, &provider.Failure{Kind: provider.InvalidResponse}
			}
			recognized = recognized || code != ""
		}
		if !recognized {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return true, nil
	}
	if fault.ErrorCode != "" || fault.Error.Code != "" {
		return false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return false, nil
}
