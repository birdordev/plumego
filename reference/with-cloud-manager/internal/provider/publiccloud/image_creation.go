package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

const imageMarkerTag = "plumego-cmp-image"

func (c *Client) imageCreationMarker(scope provider.Scope, spec provider.ImageSpec, token string) (string, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity || scope.Kind != provider.Image ||
		!slices.Contains(c.profile.Regions, scope.Region) {
		return "", &provider.Failure{Kind: provider.Unauthorized}
	}
	if !spec.ValidFor(c.name) || !resourceID.MatchString(token) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return sha([]byte(imageMarkerTag + "\x00" + launchMarker(scope, token) + "\x00" + spec.InstanceID + "\x00" + spec.Name)), nil
}

func (c *Client) PrepareImageCreation(ctx context.Context, scope provider.Scope, spec provider.ImageSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	marker, err := c.imageCreationMarker(scope, spec, token)
	if err != nil {
		return nil, err
	}
	if !c.profile.AllowImageCreation {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	ready, err := c.readyImageSource(ctx, scope.Region, spec.InstanceID)
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.createTencentImage(ctx, scope.Region, spec, marker)
		case provider.AlibabaECS:
			return c.createAlibabaImage(ctx, scope.Region, spec, token, marker)
		default:
			return c.createHuaweiImage(ctx, scope.Region, spec, marker)
		}
	}, nil
}

func (c *Client) readyImageSource(ctx context.Context, region, id string) (bool, error) {
	switch c.name {
	case provider.TencentCVM:
		row, err := c.describeTencentInstance(ctx, region, id)
		return row.InstanceState == "STOPPED" && validLaunchID(row.SystemDisk.DiskId, "disk-"), err
	case provider.AlibabaECS:
		row, err := c.describeAlibabaInstance(ctx, region, id)
		if err != nil || row.Status != "Stopped" || len(row.OperationLocks.LockReason) != 0 {
			return false, err
		}
		disks, err := c.describeAlibabaDisks(ctx, region, url.Values{"InstanceId": {id}})
		if err != nil || len(disks) != 1 {
			return false, err
		}
		disk := disks[0]
		hosts := disk.hosts()
		return disk.RegionId == region && disk.Type == "system" && disk.cloudDisk() &&
			validLaunchID(disk.DiskId, "d-") && disk.Status == "In_use" && len(hosts) == 1 && hosts[0] == id, nil
	default:
		row, err := c.describeHuaweiInstance(ctx, region, id)
		if err != nil || row.Status != "SHUTOFF" || row.Locked == nil || *row.Locked {
			return false, err
		}
		roots := 0
		for _, vol := range row.Volumes {
			if vol.BootIndex == "0" && huaweiResourceID.MatchString(vol.ID) {
				roots++
			}
		}
		return roots == 1, nil
	}
}

func (c *Client) createTencentImage(ctx context.Context, region string, spec provider.ImageSpec, marker string) (provider.LaunchReceipt, error) {
	type tagGroup struct {
		ResourceType string
		Tags         []tag
	}
	body, err := json.Marshal(struct {
		InstanceId, ImageName, ImageDescription, ForcePoweroff string
		TagSpecification                                       []tagGroup
	}{spec.InstanceID, spec.Name, marker, "false", []tagGroup{{"image", []tag{{Key: imageMarkerTag, Value: marker}}}}})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct{ ImageId, RequestId string }
	if err := c.tencent(ctx, "cvm", region, "CreateImage", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return c.imageReceipt(result.ImageId, result.RequestId)
}

func (c *Client) createAlibabaImage(ctx context.Context, region string, spec provider.ImageSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "InstanceId": {spec.InstanceID}, "ImageName": {spec.Name},
		"Description": {marker}, "ClientToken": {token}, "Tag.1.Key": {imageMarkerTag}, "Tag.1.Value": {marker}}
	var result struct{ ImageId, RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "CreateImage", "2014-05-26", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return c.imageReceipt(result.ImageId, result.RequestId)
}

func (c *Client) imageReceipt(id, requestID string) (provider.LaunchReceipt, error) {
	requestID, err := operationReceipt(requestID)
	if err != nil || !provider.ValidOwnedImage(c.name, id) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.LaunchReceipt{RequestID: requestID, ResourceID: id}, nil
}

func (c *Client) createHuaweiImage(ctx context.Context, region string, spec provider.ImageSpec, marker string) (provider.LaunchReceipt, error) {
	type imageTag struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	body, err := json.Marshal(struct {
		Name        string     `json:"name"`
		InstanceID  string     `json:"instance_id"`
		Description string     `json:"description"`
		Tags        []imageTag `json:"image_tags"`
	}{spec.Name, spec.InstanceID, marker, []imageTag{{imageMarkerTag, marker}}})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := huaweiIMS(region, "/v2.1/cloudimages/action")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Job string `json:"job_id"`
	}
	if err := c.request(req, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	job, err := operationReceipt(result.Job)
	return provider.LaunchReceipt{RequestID: job}, err
}
