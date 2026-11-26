package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentImages(ctx context.Context, region, cursor string) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	type filter struct {
		Name   string
		Values []string
	}
	const private = "PRIVATE_IMAGE"
	body, err := json.Marshal(struct {
		Offset, Limit int
		Filters       []filter
	}{
		Offset: offset, Limit: pageSize,
		Filters: []filter{{Name: "image-type", Values: []string{private}}},
	})
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount *int
		ImageSet   []struct {
			ImageId, ImageName, ImageState, ImageType string
			Tags                                      []*tag
		}
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeImages", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	if result.ImageSet == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.ImageSet), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(result.ImageSet))}
	for _, row := range result.ImageSet {
		if row.ImageType != private {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !strings.HasPrefix(row.ImageId, "img-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, tagObject(row.ImageId, row.ImageName, row.ImageState, row.Tags))
	}
	return page, nil
}

func (c *Client) alibabaImages(ctx context.Context, region, cursor string) (provider.Page, error) {
	number, err := pageNumber(cursor, 1)
	if err != nil {
		return provider.Page{}, err
	}
	query := url.Values{
		"RegionId": {region}, "ImageOwnerAlias": {"self"},
		"PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(pageSize)},
	}
	var result struct {
		alibabaNumberedPage
		Code, RegionId string
		Images         struct {
			Image []struct {
				ImageId, ImageName, Status, ImageOwnerAlias string
				Tags                                        alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeImages", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	if result.RegionId != region {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.Images.Image
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := result.next(number, pageSize, len(rows))
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.ImageOwnerAlias != "self" {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		page.Objects = append(page.Objects, tagObject(row.ImageId, row.ImageName, row.Status, row.Tags.normalized()))
	}
	return page, nil
}

func (c *Client) huaweiImages(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}, "owner": {c.profile.ProjectID}}
	if cursor != "" {
		if !huaweiResourceID.MatchString(cursor) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		query.Set("marker", cursor)
	}
	endpoint := huaweiIMS(region, "/v2/cloudimages")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Images []struct {
			ID, Name, Status, Owner string
		}
		PageInfo struct {
			Next string `json:"next_marker"`
		} `json:"page_info"`
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	rows, next := result.Images, result.PageInfo.Next
	if rows == nil || len(rows) > pageSize ||
		(next != "" && (len(rows) == 0 || next != rows[len(rows)-1].ID)) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.Owner != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, provider.Object{ID: row.ID, Name: row.Name, Status: row.Status})
	}
	return page, nil
}
