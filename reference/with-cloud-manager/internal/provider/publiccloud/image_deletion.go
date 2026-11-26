package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type imageState struct {
	present   bool
	deletable bool
	copyable  bool
	object    provider.Object
}

func (c *Client) checkImageDeletionScope(scope provider.Scope, id string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity ||
		scope.Kind != provider.Image || !slices.Contains(c.profile.Regions, scope.Region) {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if !provider.ValidOwnedImage(c.name, id) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) PrepareImageDeletion(ctx context.Context, scope provider.Scope, id string) (func(context.Context) (string, error), error) {
	if err := c.checkImageDeletionScope(scope, id); err != nil {
		return nil, err
	}
	if !c.profile.AllowImageDeletion {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	state, err := c.readImageState(ctx, scope.Region, id)
	if err != nil {
		return nil, err
	}
	if !state.deletable {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.deleteOwnedImage(ctx, scope.Region, id)
	}, nil
}

func (c *Client) ObserveImageDeletion(ctx context.Context, scope provider.Scope, id string) (provider.ActionResult, error) {
	if err := c.checkImageDeletionScope(scope, id); err != nil {
		return provider.ActionPending, err
	}
	state, err := c.readImageState(ctx, scope.Region, id)
	if err != nil || state.present {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) readImageState(ctx context.Context, region, id string) (imageState, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.readTencentImageState(ctx, region, id)
	case provider.AlibabaECS:
		return c.readAlibabaImageState(ctx, region, id)
	default:
		return c.readHuaweiImageState(ctx, region, id)
	}
}

func (c *Client) readTencentImageState(ctx context.Context, region, id string) (imageState, error) {
	body, err := json.Marshal(struct{ ImageIds []string }{[]string{id}})
	if err != nil {
		return imageState{}, err
	}
	var result struct {
		TotalCount *int
		ImageSet   []struct{ ImageId, ImageName, ImageState, ImageType string }
	}
	err = c.tencent(ctx, "cvm", region, "DescribeImages", "2017-03-12", body, &result)
	if provider.IsFailure(err, provider.Absent) {
		return imageState{}, nil
	}
	if err != nil {
		return imageState{}, err
	}
	if result.TotalCount == nil || *result.TotalCount != len(result.ImageSet) || result.ImageSet == nil || len(result.ImageSet) > 1 {
		return imageState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.ImageSet) == 0 {
		return imageState{}, nil
	}
	row := result.ImageSet[0]
	if row.ImageId != id || row.ImageType != "PRIVATE_IMAGE" {
		return imageState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	ready := row.ImageState == "NORMAL"
	return imageState{present: true, deletable: ready, copyable: ready,
		object: provider.Object{ID: row.ImageId, Name: row.ImageName, Status: row.ImageState}}, nil
}

func (c *Client) readAlibabaImageState(ctx context.Context, region, id string) (imageState, error) {
	query := url.Values{"RegionId": {region}, "ImageId": {id}, "ImageOwnerAlias": {"self"},
		"PageNumber": {"1"}, "PageSize": {"10"}}
	var result struct {
		alibabaNumberedPage
		Code, RegionId string
		Images         struct {
			Image []struct{ ImageId, ImageOwnerAlias, Status, Usage, IsSelfShared string }
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeImages", "2014-05-26", query, &result); err != nil {
		return imageState{}, err
	}
	if result.Code != "" {
		return imageState{}, cloudFailure(result.Code)
	}
	if result.RegionId != region {
		return imageState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.Images.Image
	next, err := result.next(1, 10, len(rows))
	if err != nil || rows == nil || next != "" || len(rows) > 1 {
		return imageState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return imageState{}, nil
	}
	row := rows[0]
	if row.ImageId != id || row.ImageOwnerAlias != "self" {
		return imageState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return imageState{present: true, copyable: row.Status == "Available", deletable: row.Status == "Available" && row.Usage == "none" && row.IsSelfShared == "false"}, nil
}

func (c *Client) readHuaweiImageState(ctx context.Context, region, id string) (imageState, error) {
	endpoint := huaweiIMS(region, "/v2/cloudimages")
	endpoint.RawQuery = url.Values{"id": {id}, "limit": {"2"}}.Encode()
	var result struct {
		Images []struct {
			ID, Owner, Status, Visibility string
			ImageType                     string `json:"__imagetype"`
			Protected                     *bool
			Whole                         json.RawMessage `json:"__whole_image"`
			Environment                   string          `json:"virtual_env_type"`
			DiskFormat                    string          `json:"disk_format"`
			KeyID                         string          `json:"__system__cmkid"`
		}
		PageInfo struct {
			Next string `json:"next_marker"`
		} `json:"page_info"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return imageState{}, err
	}
	if result.Images == nil || len(result.Images) > 1 ||
		(result.PageInfo.Next != "" && (len(result.Images) == 0 || result.PageInfo.Next != result.Images[0].ID)) {
		return imageState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Images) == 0 {
		return imageState{}, nil
	}
	row := result.Images[0]
	if row.ID != id || row.Owner != c.profile.ProjectID {
		return imageState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	private := row.ImageType == "private" && row.Visibility == "private"
	return imageState{present: row.Status != "deleted", deletable: private && row.Status == "active" && row.Protected != nil && !*row.Protected,
		copyable: private && row.Status == "active" && nonWholeImage(row.Whole) && row.Environment == "FusionCompute" && row.DiskFormat != "" && row.DiskFormat != "iso" && row.KeyID == ""}, nil
}

func huaweiIMS(region, path string) url.URL {
	return url.URL{Scheme: "https", Host: "ims." + region + ".myhuaweicloud.com", Path: path}
}

func (c *Client) deleteOwnedImage(ctx context.Context, region, id string) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct {
			ImageIds         []string
			DeleteBindedSnap bool
		}{[]string{id}, false})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "cvm", region, "DeleteImages", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		query := url.Values{"RegionId": {region}, "ImageId": {id}, "Force": {"false"}}
		var result struct{ Code, RequestId string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DeleteImage", "2014-05-26", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		endpoint := huaweiIMS(region, "/v2/images/"+id)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), strings.NewReader(`{"delete_backup":false}`))
		if err != nil {
			return "", err
		}
		// 禁止传输层重放删除请求。
		req.GetBody = nil
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusNoContent)
	}
}

func nonWholeImage(value json.RawMessage) bool {
	return len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == `"false"`
}
