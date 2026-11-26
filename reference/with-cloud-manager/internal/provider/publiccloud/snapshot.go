package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func snapshotObject(id, name, status, source, zone, class string, size int64) provider.Object {
	if name == "" {
		name = id
	}
	object := provider.Object{ID: id, Name: name, Status: status, Snapshot: &provider.SnapshotDetails{Class: class, SizeGiB: size}}
	object.Link(provider.Volume, source, "snapshot_of")
	object.Link(provider.Zone, zone, "in_zone")
	return object
}

func (c *Client) tencentSnapshots(ctx context.Context, region, cursor string, lookup *snapshotLookup) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	filters := []tencentNetworkFilter{{"snapshot-type", []string{"PRIVATE_SNAPSHOT"}}}
	if lookup != nil {
		if lookup.id != "" {
			filters = append(filters, tencentNetworkFilter{"snapshot-id", []string{lookup.id}})
		} else {
			filters = append(filters, tencentNetworkFilter{"disk-id", []string{lookup.spec.VolumeID}},
				tencentNetworkFilter{"snapshot-name", []string{lookup.spec.Name}})
		}
	}
	body, err := json.Marshal(struct {
		Offset, Limit     int
		Order, OrderField string
		Filters           []tencentNetworkFilter
	}{offset, pageSize, "ASC", "CREATE_TIME", filters})
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount  *int
		SnapshotSet []struct {
			SnapshotId, SnapshotName, SnapshotState, SnapshotType, DiskId string
			Placement                                                     struct{ Zone string }
			CopyFromRemote                                                bool
			DiskSize                                                      int64
			Percent                                                       *int
			Tags                                                          []*tag
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeSnapshots", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	rows := result.SnapshotSet
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(rows), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.SnapshotType != "PRIVATE_SNAPSHOT" {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !validLaunchID(row.SnapshotId, "snap-") || (row.DiskId != "" && !validLaunchID(row.DiskId, "disk-")) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		source := row.DiskId
		if row.CopyFromRemote || row.SnapshotState == "COPYING_FROM_REMOTE" {
			source = ""
		}
		object := snapshotObject(row.SnapshotId, row.SnapshotName, row.SnapshotState,
			source, row.Placement.Zone, row.SnapshotType, row.DiskSize)
		if lookup != nil {
			if err := lookup.accept(object, matchingTag(row.Tags, snapshotIntentTag, lookup.marker),
				row.SnapshotState == "NORMAL" && row.Percent != nil && *row.Percent == 100); err != nil {
				return provider.Page{}, err
			}
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func (c *Client) alibabaSnapshots(ctx context.Context, region, cursor string, lookup *snapshotLookup) (provider.Page, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(pageSize)}}
	if lookup != nil {
		if lookup.id != "" {
			ids, err := json.Marshal([]string{lookup.id})
			if err != nil {
				return provider.Page{}, err
			}
			query.Set("SnapshotIds", string(ids))
		} else {
			query.Set("DiskId", lookup.spec.VolumeID)
			query.Set("SnapshotName", lookup.spec.Name)
			query.Set("Tag.1.Key", snapshotIntentTag)
			query.Set("Tag.1.Value", lookup.marker)
		}
	}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, NextToken string
		Snapshots       struct {
			Snapshot []struct {
				SnapshotId, SnapshotName, Status, RegionId, SourceDiskId, SourceRegionId, SourceStorageType, Category string
				SourceDiskSize                                                                                        json.Number
				SourceSnapshotId                                                                                      string
				Available                                                                                             json.RawMessage
				Tags                                                                                                  alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSnapshots", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	rows := result.Snapshots.Snapshot
	if rows == nil || len(rows) > pageSize || (len(rows) == 0 && result.NextToken != "") {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: result.NextToken, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.RegionId != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !validLaunchID(row.SnapshotId, "s-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		var source string
		if (row.SourceRegionId == "" || row.SourceRegionId == region) &&
			(row.SourceStorageType == "" || row.SourceStorageType == "disk") && row.SourceDiskId != "" {
			if !validLaunchID(row.SourceDiskId, "d-") {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			source = row.SourceDiskId
		}
		size, err := snapshotSize(row.SourceDiskSize)
		if err != nil {
			return provider.Page{}, err
		}
		object := snapshotObject(row.SnapshotId, row.SnapshotName, row.Status, source, "", row.Category, size)
		if lookup != nil {
			if row.SourceSnapshotId != "" {
				object.Links = nil
			}
			ready := row.Status == "accomplished" && (len(row.Available) == 0 || snapshotAvailable(row.Available))
			if err := lookup.accept(object, matchingTag(row.Tags.normalized(), snapshotIntentTag, lookup.marker), ready); err != nil {
				return provider.Page{}, err
			}
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func snapshotAvailable(value json.RawMessage) bool {
	return string(value) == "true" || string(value) == `"true"`
}

func snapshotSize(value json.Number) (int64, error) {
	if value == "" {
		return 0, nil
	}
	size, err := value.Int64()
	if err != nil || size < 0 {
		return 0, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return size, nil
}

func (c *Client) huaweiSnapshots(ctx context.Context, region, cursor string, lookup *snapshotLookup) (provider.Page, error) {
	mode := c.profile.HuaweiSnapshotAPI
	if mode == "" {
		mode = "v2"
	}
	var position string
	if cursor != "" {
		var ok bool
		position, ok = strings.CutPrefix(cursor, mode+":")
		if !ok || position == "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}
	const limit = 50
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	collection := "/cloudsnapshots/detail"
	var offset int
	if mode == "v5" {
		collection = "/snapshots/detail"
		query.Set("sort_key", "id")
		query.Set("sort_dir", "asc")
		if position != "" {
			if !huaweiResourceID.MatchString(position) {
				return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
			}
			query.Set("marker", position)
		}
	} else {
		var err error
		offset, err = pageNumber(position, 0)
		if err != nil {
			return provider.Page{}, err
		}
		query.Set("offset", strconv.Itoa(offset))
	}
	if lookup != nil {
		if lookup.id != "" {
			query.Set("id", lookup.id)
		} else {
			query.Set("volume_id", lookup.spec.VolumeID)
			query.Set("name", lookup.spec.Name)
		}
	}
	endpoint := huaweiEVS(region, "/"+mode+"/"+c.profile.ProjectID+collection)
	endpoint.RawQuery = query.Encode()
	var result struct {
		Count     *int
		Snapshots []struct {
			ID, Name, Status, Category, Description string
			Metadata                                map[string]string
			SnapshotType                            string      `json:"snapshot_type"`
			VolumeID                                string      `json:"volume_id"`
			Zone                                    string      `json:"availability_zone"`
			Project                                 string      `json:"project_id"`
			LegacyProject                           string      `json:"os-extended-snapshot-attributes:project_id"`
			Size                                    json.Number `json:"size"`
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Snapshots == nil || len(result.Snapshots) > limit {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(result.Snapshots))}
	last := position
	for _, row := range result.Snapshots {
		if (mode == "v2" && row.LegacyProject != c.profile.ProjectID) ||
			(row.Project != "" && row.Project != c.profile.ProjectID) ||
			(row.LegacyProject != "" && row.LegacyProject != c.profile.ProjectID) {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) || (row.VolumeID != "" && !huaweiResourceID.MatchString(row.VolumeID)) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if mode == "v5" && row.ID <= last {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		last = row.ID
		size, err := snapshotSize(row.Size)
		if err != nil {
			return provider.Page{}, err
		}
		class := row.Category
		if mode == "v2" {
			class = "flash"
		}
		source := row.VolumeID
		if row.SnapshotType == "copy" {
			source = ""
		}
		object := snapshotObject(row.ID, row.Name, row.Status, source, row.Zone, class, size)
		if lookup != nil {
			ready := row.Status == "available" && (mode == "v2" || class == "standard")
			marked := row.Description == snapshotIntentTag+":"+lookup.marker
			if mode == "v2" {
				marked = row.Metadata[snapshotIntentTag] == lookup.marker
			}
			if err := lookup.accept(object, marked, ready); err != nil {
				return provider.Page{}, err
			}
		}
		page.Objects = append(page.Objects, object)
	}
	if mode == "v5" {
		if len(page.Objects) != 0 {
			page.Next = mode + ":" + last
		}
	} else {
		next, err := tencentNextOffset(offset, result.Count, len(result.Snapshots), limit)
		if err != nil {
			return provider.Page{}, err
		}
		if next != "" {
			page.Next = mode + ":" + next
		}
	}
	return page, nil
}
