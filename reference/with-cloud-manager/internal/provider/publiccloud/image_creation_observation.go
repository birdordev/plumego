package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) FindCreatedImage(ctx context.Context, scope provider.Scope, spec provider.ImageSpec, token string, receipt provider.LaunchReceipt) (provider.Object, error) {
	marker, err := c.imageCreationMarker(scope, spec, token)
	if err != nil {
		return provider.Object{}, err
	}
	var image provider.Object
	region := scope.Region
	switch c.name {
	case provider.TencentCVM:
		image, err = c.findTencentImage(ctx, region, marker)
	case provider.AlibabaECS:
		image, err = c.findAlibabaImage(ctx, region, marker)
	default:
		if receipt.RequestID != "" {
			id, err := c.huaweiImageJob(ctx, region, spec.Name, receipt.RequestID, "imsCreateImageByInstance")
			if err != nil {
				return provider.Object{}, err
			}
			if receipt.ResourceID != "" && receipt.ResourceID != id {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			receipt.ResourceID = id
		}
		image, err = c.findHuaweiImage(ctx, region, marker, receipt.ResourceID)
	}
	if err != nil || image.ID == "" {
		return provider.Object{}, err
	}
	if !provider.ValidOwnedImage(c.name, image.ID) || image.Name != spec.Name ||
		(receipt.ResourceID != "" && image.ID != receipt.ResourceID) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	image.Links = []provider.Link{spec.Source()}
	return image, nil
}

func (c *Client) findTencentImage(ctx context.Context, region, marker string) (provider.Object, error) {
	type filter struct {
		Name   string
		Values []string
	}
	body, err := json.Marshal(struct {
		Filters []filter
		Limit   int
	}{[]filter{{"image-type", []string{"PRIVATE_IMAGE"}}, {"tag:" + imageMarkerTag, []string{marker}}}, 2})
	if err != nil {
		return provider.Object{}, err
	}
	var result struct {
		TotalCount *int
		ImageSet   []struct {
			ImageId, ImageName, ImageState, ImageType, ImageDescription string
			Tags                                                        []*tag
		}
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeImages", "2017-03-12", body, &result); err != nil {
		return provider.Object{}, err
	}
	if result.ImageSet == nil || result.TotalCount == nil || *result.TotalCount != len(result.ImageSet) || len(result.ImageSet) > 1 {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.ImageSet) == 0 {
		return provider.Object{}, nil
	}
	row := result.ImageSet[0]
	if row.ImageType != "PRIVATE_IMAGE" || row.ImageDescription != marker || !matchingTag(row.Tags, imageMarkerTag, marker) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.Object{ID: row.ImageId, Name: row.ImageName, Status: row.ImageState}, nil
}

func (c *Client) findAlibabaImage(ctx context.Context, region, marker string) (provider.Object, error) {
	query := url.Values{"RegionId": {region}, "ImageOwnerAlias": {"self"}, "PageNumber": {"1"}, "PageSize": {"10"},
		"Tag.1.Key": {imageMarkerTag}, "Tag.1.Value": {marker}}
	var result struct {
		alibabaNumberedPage
		Code, RegionId string
		Images         struct {
			Image []struct {
				ImageId, ImageName, ImageOwnerAlias, Status, Description string
				Tags                                                     alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeImages", "2014-05-26", query, &result); err != nil {
		return provider.Object{}, err
	}
	if result.Code != "" {
		return provider.Object{}, cloudFailure(result.Code)
	}
	if result.RegionId != region {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.Images.Image
	next, err := result.next(1, 10, len(rows))
	if err != nil || rows == nil || len(rows) > 1 || next != "" {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return provider.Object{}, nil
	}
	row := rows[0]
	if row.ImageOwnerAlias != "self" || row.Description != marker || !matchingTag(row.Tags.normalized(), imageMarkerTag, marker) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.Object{ID: row.ImageId, Name: row.ImageName, Status: row.Status}, nil
}

func (c *Client) huaweiImageJob(ctx context.Context, region, name, id, kind string) (string, error) {
	if !resourceID.MatchString(id) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	var job struct {
		ID       string `json:"job_id"`
		Type     string `json:"job_type"`
		Status   string
		Entities struct {
			ID      string            `json:"image_id"`
			Name    string            `json:"image_name"`
			SubJobs []json.RawMessage `json:"sub_jobs_result"`
		}
	}
	if err := c.readHuawei(ctx, huaweiIMS(region, "/v1/cloudimages/job/"+id), &job); err != nil {
		return "", err
	}
	if job.ID != id || job.Type != kind || len(job.Entities.SubJobs) != 0 ||
		(job.Entities.Name != "" && job.Entities.Name != name) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch job.Status {
	case "INIT", "RUNNING":
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	case "SUCCESS":
		if job.Entities.Name == name && provider.ValidOwnedImage(c.name, job.Entities.ID) {
			return job.Entities.ID, nil
		}
	}
	return "", &provider.Failure{Kind: provider.InvalidResponse}
}

func (c *Client) findHuaweiImage(ctx context.Context, region, marker, id string) (provider.Object, error) {
	query := url.Values{"owner": {c.profile.ProjectID}, "tag": {imageMarkerTag + "=" + marker}, "limit": {"2"}}
	if id != "" {
		query.Set("id", id)
	}
	endpoint := huaweiIMS(region, "/v2/cloudimages")
	endpoint.RawQuery = query.Encode()
	var result huaweiCreatedImagePage
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.Object{}, err
	}
	rows := result.Images
	if rows == nil || len(rows) > 1 || (result.PageInfo.Next != "" && (len(rows) != 1 || result.PageInfo.Next != rows[0].ID)) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return provider.Object{}, nil
	}
	row := rows[0]
	if row.Owner != c.profile.ProjectID || row.Type != "private" || row.Visibility != "private" || row.Description != marker {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if id == "" && result.PageInfo.Next != "" {
		// 无任务回执时须确认整段标记查询只匹配一张镜像。
		query.Set("marker", result.PageInfo.Next)
		endpoint.RawQuery = query.Encode()
		var next huaweiCreatedImagePage
		if err := c.readHuawei(ctx, endpoint, &next); err != nil {
			return provider.Object{}, err
		}
		if next.Images == nil || len(next.Images) != 0 || next.PageInfo.Next != "" {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	return provider.Object{ID: row.ID, Name: row.Name, Status: row.Status}, nil
}

type huaweiCreatedImagePage struct {
	Images []struct {
		ID, Name, Owner, Status, Visibility string
		Type                                string `json:"__imagetype"`
		Description                         string `json:"__description"`
	}
	PageInfo struct {
		Next string `json:"next_marker"`
	} `json:"page_info"`
}
