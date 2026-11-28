package publiccloud

import (
	"context"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const tencentRecoveryTag = "plumego-cmp-db-restore"

type tencentCDBTag struct {
	TagKey   string
	TagValue []string
}

type tencentCDBCloneRequest struct {
	InstanceId, SpecifiedRollbackTime, InstanceName string
	UniqVpcId, UniqSubnetId, Zone, SlaveZone        string
	DeviceType, PayType                             string
	Memory, Cpu, Volume, InstanceNodes              int
	DeployMode                                      int
	SecurityGroup                                   []string
	ResourceTags                                    []tencentCDBTag
	DryRun                                          bool
}

type tencentRecoveryInstance struct {
	tencentMySQLRow
	UniqSubnetId, Zone, DeviceType string
	DiskType                       *string
	InstanceType, TaskStatus       *int
	PayType, WanStatus, DeployMode *int
	InstanceNodes                  int
	Cpu, Memory, Volume            int
	TagList                        []struct{ TagKey, TagValue string }
}

func (c *Client) prepareTencentDatabaseRecovery(ctx context.Context, scope provider.Scope, spec provider.DatabaseRecovery, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if allowed, ok := c.profile.TencentDatabaseRecoveryPlacements[scope.Region]; !ok || allowed != spec.DatabaseRecoveryPlacement ||
		!launchTokenPattern.MatchString(token) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	source, err := c.readTencentRecoveryInstance(ctx, scope.Region, spec.SourceID, "")
	if err != nil {
		return nil, err
	}
	if !source.readyForRecovery(spec.Flavor) || source.InstanceName == spec.Name || source.UniqVpcId == spec.VPC ||
		!validLaunchID(source.UniqVpcId, "vpc-") || source.Cpu <= 0 || source.Memory <= 0 || source.Volume <= 0 ||
		source.Cpu > spec.CPU || source.Memory > spec.MemoryMiB || source.Volume > spec.StorageGiB {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	windows, err := c.tencentCDBRestoreWindows(ctx, scope.Region, spec.SourceID)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseRestoreWindows(windows); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(windows, func(window provider.DatabaseRestoreWindow) bool { return window.Contains(spec.RestoreTime) }) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	stamp, err := c.tencentRecoveryTime(spec)
	if err != nil {
		return nil, err
	}
	if err := c.checkTencentRecoveryEncryption(ctx, scope.Region, spec.SourceID); err != nil {
		return nil, err
	}
	subnet, err := c.tencentLaunchSubnet(ctx, scope.Region, spec.Subnet)
	if err != nil {
		return nil, err
	}
	if subnet.VpcId != spec.VPC || subnet.Zone != spec.Zone {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	input := tencentCDBCloneRequest{
		InstanceId: spec.SourceID, SpecifiedRollbackTime: stamp, InstanceName: spec.Name,
		UniqVpcId: spec.VPC, UniqSubnetId: spec.Subnet, Zone: spec.Zone, SlaveZone: spec.Zone, DeviceType: spec.Flavor,
		PayType: "USED_PAID", Memory: spec.MemoryMiB, Cpu: spec.CPU, Volume: spec.StorageGiB, InstanceNodes: 2,
		SecurityGroup: []string{spec.SecurityGroup}, DryRun: true,
		ResourceTags: []tencentCDBTag{{TagKey: tencentRecoveryTag, TagValue: []string{tencentRecoveryMarker(scope, token)}}},
	}
	var preflight struct{ RequestId, AsyncRequestId string }
	if err := c.tencentRestoreWindow(ctx, "cdb", scope.Region, "CreateCloneInstance", "2017-03-20", input, &preflight); err != nil {
		return nil, err
	}
	if !resourceID.MatchString(preflight.RequestId) || preflight.AsyncRequestId != "" {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	input.DryRun = false
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		var result struct{ AsyncRequestId string }
		if err := c.tencentRestoreWindow(ctx, "cdb", scope.Region, "CreateCloneInstance", "2017-03-20", input, &result); err != nil ||
			!resourceID.MatchString(result.AsyncRequestId) {
			return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		return provider.LaunchReceipt{RequestID: result.AsyncRequestId}, nil
	}, nil
}

func tencentRecoveryMarker(scope provider.Scope, token string) string {
	return sha([]byte(tencentRecoveryTag + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func (c *Client) tencentRecoveryTime(spec provider.DatabaseRecovery) (string, error) {
	zone, err := c.databaseBackupTimeZone(spec.SourceID)
	if err != nil {
		return "", err
	}
	local := spec.RestoreTime.In(zone)
	stamp := local.Format(time.DateTime)
	_, offset := local.Zone()
	start, end := local.ZoneBounds()
	neighbors := []time.Time{end}
	if !start.IsZero() {
		neighbors = append(neighbors, start.Add(-time.Second))
	}
	for _, neighbor := range neighbors {
		if neighbor.IsZero() {
			continue
		}
		_, adjacentOffset := neighbor.In(zone).Zone()
		if adjacentOffset != offset && local.Add(time.Duration(offset-adjacentOffset)*time.Second).Format(time.DateTime) == stamp {
			return "", &provider.Failure{Kind: provider.CreationBlocked}
		}
	}
	return stamp, nil
}

func (row tencentRecoveryInstance) readyForRecovery(flavor string) bool {
	return row.Status != nil && *row.Status == 1 && row.TaskStatus != nil && *row.TaskStatus == 0 &&
		row.InstanceType != nil && *row.InstanceType == 1 && row.WanStatus != nil && (*row.WanStatus == 0 || *row.WanStatus == 2) &&
		row.EngineVersion != "" && row.DeviceType == flavor && row.DiskType != nil && *row.DiskType == ""
}

func (c *Client) readTencentRecoveryInstance(ctx context.Context, region, id, marker string) (tencentRecoveryInstance, error) {
	type tagFilter struct{ Key, Value string }
	input := struct {
		InstanceIds []string    `json:",omitempty"`
		Tags        []tagFilter `json:",omitempty"`
		Limit       int
	}{Limit: 2}
	if id != "" {
		input.InstanceIds = []string{id}
	} else {
		input.Tags = []tagFilter{{Key: tencentRecoveryTag, Value: marker}}
	}
	var result struct {
		TotalCount *int
		Items      []tencentRecoveryInstance
	}
	if err := c.tencentDatabasePage(ctx, "cdb", region, "2017-03-20", input, &result); err != nil {
		return tencentRecoveryInstance{}, err
	}
	if result.TotalCount == nil || result.Items == nil || *result.TotalCount != len(result.Items) || len(result.Items) > 1 {
		return tencentRecoveryInstance{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Items) == 0 {
		return tencentRecoveryInstance{}, &provider.Failure{Kind: provider.Absent}
	}
	row := result.Items[0]
	if row.Region != region || (id != "" && row.InstanceId != id) || !provider.ValidDatabaseRecoveryInstanceID(c.name, row.InstanceId) ||
		(marker != "" && !matchesTencentRecoveryTag(row.TagList, marker)) {
		return tencentRecoveryInstance{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return row, nil
}

func (c *Client) checkTencentRecoveryEncryption(ctx context.Context, region, id string) error {
	var result struct{ InstanceId, Encryption string }
	if err := c.tencentRestoreWindow(ctx, "cdb", region, "DescribeDBInstanceInfo", "2017-03-20", struct{ InstanceId string }{id}, &result); err != nil {
		return err
	}
	if result.InstanceId != id || strings.ToUpper(result.Encryption) != "NO" {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func matchesTencentRecoveryTag(tags []struct{ TagKey, TagValue string }, marker string) bool {
	found := false
	for _, tag := range tags {
		if tag.TagKey == tencentRecoveryTag {
			if found || tag.TagValue != marker {
				return false
			}
			found = true
		}
	}
	return found
}
