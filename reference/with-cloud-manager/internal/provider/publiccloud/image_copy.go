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

func (c *Client) imageCopyTarget(source, destination provider.Scope, observer provider.ImageCopier, spec provider.ImageCopySpec, token string) (*Client, string, error) {
	target, ok := observer.(*Client)
	if !ok || target == nil || c.name != target.name || c.profile.AccountID != target.profile.AccountID ||
		source.Region == destination.Region {
		return nil, "", &provider.Failure{Kind: provider.Unauthorized}
	}
	if err := c.checkImageDeletionScope(source, spec.ImageID); err != nil {
		return nil, "", err
	}
	if destination.Kind != provider.Image || destination.Provider != target.name || destination.RemoteIdentity != target.identity ||
		!slices.Contains(target.profile.Regions, destination.Region) {
		return nil, "", &provider.Failure{Kind: provider.Unauthorized}
	}
	if !spec.ValidFor(c.name) || !resourceID.MatchString(token) {
		return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if c.name == provider.HuaweiECS && (c.profile.ProjectID == target.profile.ProjectID || !resourceID.MatchString(target.projectName)) {
		return nil, "", &provider.Failure{Kind: provider.Unauthorized}
	}
	marker := sha([]byte("copy-image\x00" + launchMarker(source, token) + "\x00" + destination.RemoteIdentity + "\x00" +
		destination.Region + "\x00" + spec.ImageID + "\x00" + spec.Name))
	return target, marker, nil
}

func (c *Client) PrepareImageCopy(ctx context.Context, source, destination provider.Scope, observer provider.ImageCopier, spec provider.ImageCopySpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	target, marker, err := c.imageCopyTarget(source, destination, observer, spec, token)
	if err != nil {
		return nil, err
	}
	if !c.profile.AllowImageCopy || (c.name == provider.HuaweiECS && !resourceID.MatchString(c.profile.HuaweiImageCopyAgency)) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	state, err := c.readImageState(ctx, source.Region, spec.ImageID)
	if err != nil {
		return nil, err
	}
	if !state.copyable {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.copyTencentImage(ctx, source.Region, destination.Region, spec)
		case provider.AlibabaECS:
			return c.copyAlibabaImage(ctx, source.Region, destination.Region, spec, token, marker)
		default:
			return c.copyHuaweiImage(ctx, source.Region, destination.Region, target.projectName, spec, marker)
		}
	}, nil
}

func (c *Client) copyTencentImage(ctx context.Context, source, destination string, spec provider.ImageCopySpec) (provider.LaunchReceipt, error) {
	body, err := json.Marshal(struct {
		ImageIds, DestinationRegions []string
		ImageName                    string
		ImageSetRequired             bool
	}{[]string{spec.ImageID}, []string{destination}, spec.Name, true})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		RequestId string
		ImageSet  []struct{ Region, ImageId string }
	}
	if err := c.tencent(ctx, "cvm", source, "SyncImages", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if len(result.ImageSet) != 1 || result.ImageSet[0].Region != destination {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return c.imageReceipt(result.ImageSet[0].ImageId, result.RequestId)
}

func (c *Client) copyAlibabaImage(ctx context.Context, source, destination string, spec provider.ImageCopySpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {source}, "ImageId": {spec.ImageID}, "DestinationRegionId": {destination},
		"DestinationImageName": {spec.Name}, "DestinationDescription": {marker}, "ClientToken": {token},
		"Tag.1.Key": {imageMarkerTag}, "Tag.1.Value": {marker}}
	var result struct{ ImageId, RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+source+".aliyuncs.com", "CopyImage", "2014-05-26", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return c.imageReceipt(result.ImageId, result.RequestId)
}

func (c *Client) copyHuaweiImage(ctx context.Context, source, destination, project string, spec provider.ImageCopySpec, marker string) (provider.LaunchReceipt, error) {
	body, err := json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Region      string `json:"region"`
		Project     string `json:"project_name"`
		Agency      string `json:"agency_name"`
	}{spec.Name, marker, destination, project, c.profile.HuaweiImageCopyAgency})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := huaweiIMS(source, "/v1/cloudimages/"+spec.ImageID+"/cross_region_copy")
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

func (c *Client) FindCopiedImage(ctx context.Context, source, destination provider.Scope, observer provider.ImageCopier, spec provider.ImageCopySpec, token string, receipt provider.LaunchReceipt) (provider.Object, error) {
	target, marker, err := c.imageCopyTarget(source, destination, observer, spec, token)
	if err != nil {
		return provider.Object{}, err
	}
	var image provider.Object
	switch c.name {
	case provider.TencentCVM:
		if !provider.ValidOwnedImage(c.name, receipt.ResourceID) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		state, err := target.readTencentImageState(ctx, destination.Region, receipt.ResourceID)
		if err != nil || !state.present {
			return provider.Object{}, err
		}
		image = state.object
	case provider.AlibabaECS:
		image, err = target.findAlibabaImage(ctx, destination.Region, marker)
	default:
		if receipt.RequestID != "" {
			id, err := c.huaweiImageJob(ctx, source.Region, spec.Name, receipt.RequestID, "imsCrossRegionCopyImageJob")
			if err != nil {
				return provider.Object{}, err
			}
			if receipt.ResourceID != "" && receipt.ResourceID != id {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			receipt.ResourceID = id
		}
		image, err = target.findHuaweiCopiedImage(ctx, destination.Region, spec.Name, marker, receipt.ResourceID)
	}
	if err != nil || image.ID == "" {
		return provider.Object{}, err
	}
	if !provider.ValidOwnedImage(c.name, image.ID) || image.Name != spec.Name || image.ID == spec.ImageID ||
		(receipt.ResourceID != "" && image.ID != receipt.ResourceID) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return image, nil
}

func (c *Client) findHuaweiCopiedImage(ctx context.Context, region, name, marker, id string) (provider.Object, error) {
	query := url.Values{"owner": {c.profile.ProjectID}, "name": {name}, "limit": {"100"}}
	if id != "" {
		query.Set("id", id)
	}
	endpoint := huaweiIMS(region, "/v2/cloudimages")
	var matched provider.Object
	seen := make(map[string]bool)
	for page := 0; page < 4; page++ {
		endpoint.RawQuery = query.Encode()
		var result huaweiCreatedImagePage
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return provider.Object{}, err
		}
		rows, next := result.Images, result.PageInfo.Next
		if rows == nil || len(rows) > 100 || (next != "" && (len(rows) == 0 || next != rows[len(rows)-1].ID)) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range rows {
			if !provider.ValidOwnedImage(c.name, row.ID) || seen[row.ID] || row.Owner != c.profile.ProjectID || row.Name != name ||
				(id != "" && (row.ID != id || row.Description != marker)) {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			seen[row.ID] = true
			if row.Description != marker {
				continue
			}
			if matched.ID != "" || row.Type != "private" || row.Visibility != "private" {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			matched = provider.Object{ID: row.ID, Name: row.Name, Status: row.Status}
		}
		if next == "" || id != "" {
			return matched, nil
		}
		query.Set("marker", next)
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}
