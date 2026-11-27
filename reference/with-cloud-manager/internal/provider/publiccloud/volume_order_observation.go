package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type provisionedVolume struct {
	id, name, status, zone, diskType, snapshot string
	size                                       int
	marked, ready                              bool
	encrypted                                  *bool
}

type volumeLookup struct {
	id, sourceVolume string
	order            volumeOrder
}

func (c *Client) FindRestoredVolume(ctx context.Context, scope provider.Scope, spec provider.SnapshotRestoreSpec, token, id string) (provider.Object, error) {
	if err := c.checkSnapshotRestoreScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	return c.findProvisionedVolume(ctx, scope, volumeLookup{id: id, sourceVolume: spec.VolumeID, order: volumeOrder{
		spec: spec.Volume(), tagKey: restoreIntentTag, snapshot: spec.ID, marker: volumeOrderMarker(scope, restoreIntentTag, token),
	}})
}

func (c *Client) findProvisionedVolume(ctx context.Context, scope provider.Scope, lookup volumeLookup) (provider.Object, error) {
	id, spec := lookup.id, lookup.order.spec
	snapshot := lookup.order.snapshot
	read, validID := c.readHuaweiProvisionedVolumes, huaweiResourceID.MatchString
	switch c.name {
	case provider.TencentCVM:
		read = c.readTencentProvisionedVolumes
		validID = func(id string) bool { return validLaunchID(id, "disk-") }
	case provider.AlibabaECS:
		read = c.readAlibabaProvisionedVolumes
		validID = func(id string) bool { return validLaunchID(id, "d-") }
	}
	if id != "" && (!validID(id) || id == lookup.sourceVolume) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var found provisionedVolume
	var cursor string
	for range 8 {
		rows, next, err := read(ctx, scope.Region, cursor, lookup)
		if err != nil {
			return provider.Object{}, err
		}
		if (id != "" && (len(rows) > 1 || next != "")) || len(next) > provider.MaxCursorBytes || (next != "" && next == cursor) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range rows {
			if !validID(row.id) || (id != "" && id != row.id) {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			if !row.marked {
				continue
			}
			if found.id != "" || row.id == lookup.sourceVolume || row.name != spec.Name || (row.snapshot != "" && row.snapshot != snapshot) {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			found = row
		}
		if next == "" {
			if !found.ready {
				return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
			}
			if found.zone != spec.Zone || found.diskType != spec.DiskType || found.size != spec.SizeGiB ||
				found.encrypted == nil || (spec.Encrypted && !*found.encrypted) ||
				(c.name != provider.TencentCVM && found.snapshot != snapshot) {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			result := provider.Object{ID: found.id, Name: found.name, Status: found.status}
			result.Link(provider.Zone, found.zone, "in_zone")
			if snapshot != "" {
				result.Link(provider.Snapshot, snapshot, "restored_from")
			} else {
				var bootable *bool
				usage := "data"
				if c.name == provider.HuaweiECS {
					value := false
					bootable, usage = &value, ""
				}
				result.Volume = volumeInventoryDetails(&found.size, found.diskType, usage, bootable)
			}
			return result, nil
		}
		cursor = next
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}

func (c *Client) readTencentProvisionedVolumes(ctx context.Context, region, cursor string, lookup volumeLookup) ([]provisionedVolume, string, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return nil, "", err
	}
	query := struct {
		Offset, Limit int
		DiskIds       []string               `json:",omitempty"`
		Filters       []tencentNetworkFilter `json:",omitempty"`
	}{Offset: offset, Limit: pageSize}
	if lookup.id != "" {
		query.DiskIds = []string{lookup.id}
	} else {
		query.Filters = []tencentNetworkFilter{{"tag:" + lookup.order.tagKey, []string{lookup.order.marker}}}
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, "", err
	}
	var result struct {
		TotalCount *int
		DiskSet    []struct {
			DiskId, DiskName, DiskState, DiskType, InstanceId, DiskUsage, DiskChargeType, SnapshotId string
			DiskSize                                                                                 int
			Placement                                                                                struct{ Zone string }
			Tags                                                                                     []*tag
			InstanceIdList                                                                           []string
			Portable, Shareable, Attached                                                            *bool
			Encrypt                                                                                  *bool
		}
	}
	if err := c.tencent(ctx, "cbs", region, "DescribeDisks", "2017-03-12", body, &result); err != nil {
		return nil, "", err
	}
	if result.DiskSet == nil {
		return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.DiskSet), pageSize)
	if err != nil {
		return nil, "", err
	}
	rows := make([]provisionedVolume, 0, len(result.DiskSet))
	for _, row := range result.DiskSet {
		standalone := row.Portable != nil && *row.Portable && row.Shareable != nil && !*row.Shareable && row.Attached != nil && !*row.Attached
		rows = append(rows, provisionedVolume{id: row.DiskId, name: row.DiskName, status: row.DiskState, zone: row.Placement.Zone,
			diskType: row.DiskType, snapshot: row.SnapshotId, size: row.DiskSize, encrypted: row.Encrypt,
			marked: matchingTag(row.Tags, lookup.order.tagKey, lookup.order.marker),
			ready: row.DiskState == "UNATTACHED" && row.DiskUsage == "DATA_DISK" && row.DiskChargeType == "POSTPAID_BY_HOUR" &&
				row.InstanceId == "" && len(row.InstanceIdList) == 0 && (lookup.order.snapshot != "" || standalone)})
	}
	return rows, next, nil
}

func (c *Client) readAlibabaProvisionedVolumes(ctx context.Context, region, cursor string, lookup volumeLookup) ([]provisionedVolume, string, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(pageSize)}}
	if lookup.id != "" {
		ids, err := json.Marshal([]string{lookup.id})
		if err != nil {
			return nil, "", err
		}
		query.Set("DiskIds", string(ids))
	} else {
		if lookup.order.snapshot != "" {
			query.Set("SnapshotId", lookup.order.snapshot)
		}
		query.Set("Tag.1.Key", lookup.order.tagKey)
		query.Set("Tag.1.Value", lookup.order.marker)
	}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, NextToken string
		Disks           struct {
			Disk []struct {
				alibabaDiskObservation
				DiskName, SourceSnapshotId, ImageId string
				Encrypted                           *bool
				Tags                                alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeDisks", "2014-05-26", query, &result); err != nil {
		return nil, "", err
	}
	if result.Code != "" {
		return nil, "", cloudFailure(result.Code)
	}
	disks := result.Disks.Disk
	if disks == nil || len(disks) > pageSize || (len(disks) == 0 && result.NextToken != "") {
		return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	rows := make([]provisionedVolume, 0, len(disks))
	for _, row := range disks {
		if row.RegionId != region {
			return nil, "", &provider.Failure{Kind: provider.Unauthorized}
		}
		standalone := row.Portable != nil && *row.Portable && row.MultiAttach == "Disabled" && row.ImageId == ""
		size := 0
		if row.Size != nil {
			size = *row.Size
		}
		rows = append(rows, provisionedVolume{id: row.DiskId, name: row.DiskName, status: row.Status, zone: row.ZoneId,
			diskType: row.Category, snapshot: row.SourceSnapshotId, size: size, encrypted: row.Encrypted,
			marked: matchingTag(row.Tags.normalized(), lookup.order.tagKey, lookup.order.marker),
			ready:  row.Status == "Available" && row.Type == "data" && row.DiskChargeType == "PostPaid" && len(row.hosts()) == 0 && (lookup.order.snapshot != "" || standalone)})
	}
	return rows, result.NextToken, nil
}

type huaweiProvisionedVolume struct {
	ID, Name, Status, Description, Bootable string
	Project                                 string                     `json:"os-vol-tenant-attr:tenant_id"`
	Zone                                    string                     `json:"availability_zone"`
	Type                                    string                     `json:"volume_type"`
	Snapshot                                string                     `json:"snapshot_id"`
	SourceVolume                            string                     `json:"source_volid"`
	Image                                   map[string]json.RawMessage `json:"volume_image_metadata"`
	Multiattach                             *bool
	Size                                    int
	Attachments                             []json.RawMessage
	Metadata                                map[string]json.RawMessage
}

func (c *Client) readHuaweiProvisionedVolumes(ctx context.Context, region, cursor string, lookup volumeLookup) ([]provisionedVolume, string, error) {
	path := "/v2/" + c.profile.ProjectID + "/cloudvolumes"
	var disks []huaweiProvisionedVolume
	var next string
	if lookup.id != "" {
		endpoint := huaweiEVS(region, path+"/"+lookup.id)
		var result struct{ Volume huaweiProvisionedVolume }
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return nil, "", err
		}
		disks = []huaweiProvisionedVolume{result.Volume}
	} else {
		endpoint := huaweiEVS(region, path+"/detail")
		query := url.Values{"name": {lookup.order.spec.Name}, "limit": {"100"}, "sort_key": {"id"}, "sort_dir": {"asc"}}
		if cursor != "" {
			query.Set("marker", cursor)
		}
		endpoint.RawQuery = query.Encode()
		var result struct{ Volumes []huaweiProvisionedVolume }
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return nil, "", err
		}
		disks = result.Volumes
		if disks == nil || len(disks) > pageSize {
			return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		last := cursor
		for _, disk := range disks {
			if !huaweiResourceID.MatchString(disk.ID) || disk.ID <= last {
				return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
			}
			last = disk.ID
		}
		if len(disks) != 0 {
			next = last
		}
	}
	rows := make([]provisionedVolume, 0, len(disks))
	for _, disk := range disks {
		if disk.Project != c.profile.ProjectID {
			return nil, "", &provider.Failure{Kind: provider.Unauthorized}
		}
		var encrypted *bool
		switch string(disk.Metadata["__system__encrypted"]) {
		case "", `"0"`, "0":
			value := false
			encrypted = &value
		case `"1"`, "1":
			value := true
			encrypted = &value
		}
		billing := disk.Metadata["orderID"]
		standalone := disk.Multiattach != nil && !*disk.Multiattach && disk.Metadata != nil &&
			(len(billing) == 0 || string(billing) == `""`) && disk.SourceVolume == "" && len(disk.Image) == 0
		rows = append(rows, provisionedVolume{id: disk.ID, name: disk.Name, status: disk.Status, zone: disk.Zone,
			diskType: disk.Type, snapshot: disk.Snapshot, size: disk.Size, encrypted: encrypted,
			marked: disk.Description == lookup.order.tagKey+":"+lookup.order.marker,
			ready:  disk.Status == "available" && disk.Bootable == "false" && disk.Attachments != nil && len(disk.Attachments) == 0 && (lookup.order.snapshot != "" || standalone)})
	}
	return rows, next, nil
}
