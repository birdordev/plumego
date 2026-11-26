package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type restoredVolume struct {
	id, name, status, zone, diskType, snapshot string
	size                                       int
	marked, ready                              bool
	encrypted                                  *bool
}

type restoreLookup struct {
	id, marker string
	spec       provider.SnapshotRestoreSpec
}

func (c *Client) FindRestoredVolume(ctx context.Context, scope provider.Scope, spec provider.SnapshotRestoreSpec, token, id string) (provider.Object, error) {
	if err := c.checkSnapshotRestoreScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	read, validID := c.restoredHuaweiVolumes, huaweiResourceID.MatchString
	switch c.name {
	case provider.TencentCVM:
		read = c.restoredTencentVolumes
		validID = func(id string) bool { return validLaunchID(id, "disk-") }
	case provider.AlibabaECS:
		read = c.restoredAlibabaVolumes
		validID = func(id string) bool { return validLaunchID(id, "d-") }
	}
	if id != "" && (!validID(id) || id == spec.VolumeID) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	lookup := restoreLookup{id: id, marker: restoreMarker(scope, token), spec: spec}
	var found restoredVolume
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
			if found.id != "" || row.id == spec.VolumeID || row.name != spec.Name || (row.snapshot != "" && row.snapshot != spec.ID) {
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
				(c.name != provider.TencentCVM && found.snapshot != spec.ID) {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			result := provider.Object{ID: found.id, Name: found.name, Status: found.status}
			result.Link(provider.Zone, found.zone, "in_zone")
			result.Link(provider.Snapshot, spec.ID, "restored_from")
			return result, nil
		}
		cursor = next
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}

func (c *Client) restoredTencentVolumes(ctx context.Context, region, cursor string, lookup restoreLookup) ([]restoredVolume, string, error) {
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
		query.Filters = []tencentNetworkFilter{{"tag:" + restoreIntentTag, []string{lookup.marker}}}
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
	rows := make([]restoredVolume, 0, len(result.DiskSet))
	for _, row := range result.DiskSet {
		rows = append(rows, restoredVolume{id: row.DiskId, name: row.DiskName, status: row.DiskState, zone: row.Placement.Zone,
			diskType: row.DiskType, snapshot: row.SnapshotId, size: row.DiskSize, encrypted: row.Encrypt,
			marked: matchingTag(row.Tags, restoreIntentTag, lookup.marker),
			ready: row.DiskState == "UNATTACHED" && row.DiskUsage == "DATA_DISK" && row.DiskChargeType == "POSTPAID_BY_HOUR" &&
				row.InstanceId == "" && len(row.InstanceIdList) == 0})
	}
	return rows, next, nil
}

func (c *Client) restoredAlibabaVolumes(ctx context.Context, region, cursor string, lookup restoreLookup) ([]restoredVolume, string, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(pageSize)}}
	if lookup.id != "" {
		ids, err := json.Marshal([]string{lookup.id})
		if err != nil {
			return nil, "", err
		}
		query.Set("DiskIds", string(ids))
	} else {
		query.Set("SnapshotId", lookup.spec.ID)
		query.Set("Tag.1.Key", restoreIntentTag)
		query.Set("Tag.1.Value", lookup.marker)
	}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, NextToken string
		Disks           struct {
			Disk []struct {
				alibabaDiskObservation
				DiskName, SourceSnapshotId, DiskChargeType string
				Size                                       int
				Encrypted                                  *bool
				Tags                                       alibabaECSTags
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
	rows := make([]restoredVolume, 0, len(disks))
	for _, row := range disks {
		if row.RegionId != region {
			return nil, "", &provider.Failure{Kind: provider.Unauthorized}
		}
		rows = append(rows, restoredVolume{id: row.DiskId, name: row.DiskName, status: row.Status, zone: row.ZoneId,
			diskType: row.Category, snapshot: row.SourceSnapshotId, size: row.Size, encrypted: row.Encrypted,
			marked: matchingTag(row.Tags.normalized(), restoreIntentTag, lookup.marker),
			ready:  row.Status == "Available" && row.Type == "data" && row.DiskChargeType == "PostPaid" && len(row.hosts()) == 0})
	}
	return rows, result.NextToken, nil
}

type huaweiRestoredVolume struct {
	ID, Name, Status, Description, Bootable string
	Project                                 string `json:"os-vol-tenant-attr:tenant_id"`
	Zone                                    string `json:"availability_zone"`
	Type                                    string `json:"volume_type"`
	Snapshot                                string `json:"snapshot_id"`
	Size                                    int
	Attachments                             []json.RawMessage
	Metadata                                map[string]json.RawMessage
}

func (c *Client) restoredHuaweiVolumes(ctx context.Context, region, cursor string, lookup restoreLookup) ([]restoredVolume, string, error) {
	path := "/v2/" + c.profile.ProjectID + "/cloudvolumes"
	var disks []huaweiRestoredVolume
	var next string
	if lookup.id != "" {
		endpoint := huaweiEVS(region, path+"/"+lookup.id)
		var result struct{ Volume huaweiRestoredVolume }
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return nil, "", err
		}
		disks = []huaweiRestoredVolume{result.Volume}
	} else {
		endpoint := huaweiEVS(region, path+"/detail")
		query := url.Values{"name": {lookup.spec.Name}, "limit": {"100"}, "sort_key": {"id"}, "sort_dir": {"asc"}}
		if cursor != "" {
			query.Set("marker", cursor)
		}
		endpoint.RawQuery = query.Encode()
		var result struct{ Volumes []huaweiRestoredVolume }
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
	rows := make([]restoredVolume, 0, len(disks))
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
		rows = append(rows, restoredVolume{id: disk.ID, name: disk.Name, status: disk.Status, zone: disk.Zone,
			diskType: disk.Type, snapshot: disk.Snapshot, size: disk.Size, encrypted: encrypted,
			marked: disk.Description == restoreIntentTag+":"+lookup.marker,
			ready:  disk.Status == "available" && disk.Bootable == "false" && disk.Attachments != nil && len(disk.Attachments) == 0})
	}
	return rows, next, nil
}
