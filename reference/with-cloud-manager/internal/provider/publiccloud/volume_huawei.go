package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) huaweiVolumeEvidence(ctx context.Context, region string, change provider.VolumeChange) (volumeEvidence, error) {
	instance, err := c.describeHuaweiInstance(ctx, region, change.InstanceID)
	if err != nil {
		return volumeEvidence{}, err
	}
	disk, err := c.describeHuaweiDisk(ctx, region, change.VolumeID)
	if err != nil {
		return volumeEvidence{}, err
	}
	if instance.Volumes == nil {
		return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	evidence := volumeEvidence{
		instanceZone: instance.Zone, volumeZone: disk.Zone, available: disk.Status == "available",
		attached: disk.Status == "in-use", safe: disk.Bootable == "false" && disk.Multiattach != nil && !*disk.Multiattach,
	}
	for _, row := range disk.Attachments {
		if row.VolumeID != change.VolumeID || !huaweiResourceID.MatchString(row.ServerID) {
			return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		evidence.hosts = append(evidence.hosts, row.ServerID)
	}
	for _, row := range instance.Volumes {
		if row.ID != change.VolumeID {
			if change.Device != "" && row.Device == change.Device {
				evidence.safe = false
			}
			continue
		}
		if evidence.instanceHasVolume {
			return volumeEvidence{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		evidence.instanceHasVolume = true
		evidence.retained = huaweiRetainsVolume(row.DeleteOnTermination)
		evidence.safe = evidence.safe && row.BootIndex == "-1" &&
			(instance.RootDevice == "" || row.Device != instance.RootDevice) &&
			(change.Device == "" || row.Device == change.Device)
	}
	return evidence, nil
}

type huaweiDiskObservation struct {
	Size                 *int
	Metadata             map[string]json.RawMessage
	ID, Status, Bootable string
	Multiattach          *bool
	ProjectID            string `json:"os-vol-tenant-attr:tenant_id"`
	Zone                 string `json:"availability_zone"`
	Attachments          []struct {
		ServerID string `json:"server_id"`
		VolumeID string `json:"volume_id"`
	}
}

func (c *Client) describeHuaweiDisk(ctx context.Context, region, id string) (huaweiDiskObservation, error) {
	u := url.URL{Scheme: "https", Host: "evs." + region + ".myhuaweicloud.com",
		Path: "/v2/" + c.profile.ProjectID + "/cloudvolumes/" + id}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return huaweiDiskObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var response struct {
		Volume huaweiDiskObservation
	}
	if err := c.request(req, &response); err != nil {
		return huaweiDiskObservation{}, err
	}
	disk := response.Volume
	if disk.ProjectID != c.profile.ProjectID {
		return huaweiDiskObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if disk.ID != id || disk.Attachments == nil {
		return huaweiDiskObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return disk, nil
}

func huaweiRetainsVolume(flag json.RawMessage) bool {
	return string(flag) == "false" || string(flag) == `"false"`
}

func (c *Client) huaweiVolumeChange(ctx context.Context, region string, change provider.VolumeChange) (string, error) {
	base := "/v1/" + c.profile.ProjectID + "/cloudservers/" + change.InstanceID
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com"}
	method := http.MethodDelete
	var body []byte
	if change.Attach {
		method = http.MethodPost
		u.Path = base + "/attachvolume"
		input := struct {
			Volume struct {
				ID     string `json:"volumeId"`
				Device string `json:"device,omitempty"`
			} `json:"volumeAttachment"`
		}{}
		input.Volume.ID, input.Volume.Device = change.VolumeID, change.Device
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return "", err
		}
	} else {
		u.Path = base + "/detachvolume/" + change.VolumeID
		u.RawQuery = "delete_flag=0"
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	if change.Attach {
		req.Header.Set("Content-Type", "application/json")
	}
	var response struct {
		JobID string `json:"job_id"`
	}
	if err := c.request(req, &response); err != nil {
		return "", err
	}
	return operationReceipt(response.JobID)
}
