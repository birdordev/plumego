package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentVolumes(ctx context.Context, region, cursor string) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	body, err := json.Marshal(struct{ Offset, Limit int }{offset, pageSize})
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount *int
		DiskSet    []struct {
			DiskId, DiskName, DiskState, InstanceId string
			DiskType, DiskUsage                     string
			DiskSize                                *int
			Placement                               struct{ Zone string }
			InstanceIdList                          []string
			Tags                                    []*tag
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	if result.DiskSet == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.DiskSet), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(result.DiskSet))}
	for _, row := range result.DiskSet {
		if !strings.HasPrefix(row.DiskId, "disk-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := tagObject(row.DiskId, row.DiskName, row.DiskState, row.Tags)
		usage := ""
		switch row.DiskUsage {
		case "SYSTEM_DISK":
			usage = "system"
		case "DATA_DISK":
			usage = "data"
		}
		item.Volume = volumeInventoryDetails(row.DiskSize, row.DiskType, usage, nil)
		item.Link(provider.Zone, row.Placement.Zone, "in_zone")
		linkVolumeInstances(&item, append(row.InstanceIdList, row.InstanceId))
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}

func (c *Client) alibabaVolumes(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, NextToken string
		Disks           struct {
			Disk []struct {
				DiskId, DiskName, Status, InstanceId, RegionId, ZoneId string
				Category, Type                                         string
				Size                                                   *int
				MountInstances                                         struct{ MountInstance []struct{ InstanceId string } }
				Tags                                                   alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeDisks", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	rows := result.Disks.Disk
	if rows == nil || len(rows) > pageSize || (len(rows) == 0 && result.NextToken != "") {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: result.NextToken, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.RegionId != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		item := tagObject(row.DiskId, row.DiskName, row.Status, row.Tags.normalized())
		usage := ""
		if row.Type == "system" || row.Type == "data" {
			usage = row.Type
		}
		item.Volume = volumeInventoryDetails(row.Size, row.Category, usage, nil)
		item.Link(provider.Zone, row.ZoneId, "in_zone")
		instances := make([]string, 0, len(row.MountInstances.MountInstance)+1)
		instances = append(instances, row.InstanceId)
		for _, mount := range row.MountInstances.MountInstance {
			instances = append(instances, mount.InstanceId)
		}
		linkVolumeInstances(&item, instances)
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}

func (c *Client) huaweiVolumes(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		if !huaweiResourceID.MatchString(cursor) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		query.Set("marker", cursor)
	}
	projectPath := "/v2/" + c.profile.ProjectID
	endpoint := url.URL{
		Scheme: "https", Host: "evs." + region + ".myhuaweicloud.com",
		Path: projectPath + "/cloudvolumes/detail", RawQuery: query.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Links   []struct{ Rel, Href string } `json:"volumes_links"`
		Volumes []struct {
			ID, Name, Status string
			Size             *int
			VolumeType       string `json:"volume_type"`
			Bootable         string
			ProjectID        string `json:"os-vol-tenant-attr:tenant_id"`
			Zone             string `json:"availability_zone"`
			Attachments      []struct {
				ServerID string `json:"server_id"`
			}
		}
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Volumes == nil || len(result.Volumes) > pageSize {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(result.Volumes))}
	for _, row := range result.Volumes {
		if row.ProjectID != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := provider.Object{ID: row.ID, Name: row.Name, Status: row.Status}
		var bootable *bool
		switch row.Bootable {
		case "true", "false":
			value := row.Bootable == "true"
			bootable = &value
		case "":
		default:
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item.Volume = volumeInventoryDetails(row.Size, row.VolumeType, "", bootable)
		item.Link(provider.Zone, row.Zone, "in_zone")
		instances := make([]string, 0, len(row.Attachments))
		for _, attachment := range row.Attachments {
			if !huaweiResourceID.MatchString(attachment.ServerID) {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			instances = append(instances, attachment.ServerID)
		}
		linkVolumeInstances(&item, instances)
		page.Objects = append(page.Objects, item)
	}
	for _, link := range result.Links {
		if link.Rel != "next" {
			continue
		}
		if page.Next != "" || len(page.Objects) == 0 {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		target, err := endpoint.Parse(link.Href)
		if err != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || target.RawPath != "" ||
			(target.Path != endpoint.Path && target.Path != projectPath+"/volumes/detail") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		parameters, err := url.ParseQuery(target.RawQuery)
		if err != nil || len(parameters["marker"]) != 1 || parameters.Get("marker") != page.Objects[len(page.Objects)-1].ID {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		// EVS 可能返回内部端点。续页只携带已校验的标记，请求继续发往区域端点。
		page.Next = parameters.Get("marker")
	}
	return page, nil
}

func volumeInventoryDetails(size *int, storageClass, usage string, bootable *bool) *provider.VolumeDetails {
	if size == nil && storageClass == "" && usage == "" && bootable == nil {
		return nil
	}
	return &provider.VolumeDetails{SizeGiB: size, StorageClass: storageClass, Usage: usage, Bootable: bootable}
}

func linkVolumeInstances(volume *provider.Object, instances []string) {
	slices.Sort(instances)
	for _, id := range slices.Compact(instances) {
		volume.Link(provider.Instance, id, "attached_to")
	}
}
