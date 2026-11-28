package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type alibabaRecoveryInstance struct {
	alibabaRDSRow
	Category, ZoneId, VSwitchId, InstanceNetworkType, PayType string
	DBInstanceClass, DBInstanceStorageType, DBInstanceType    string
	DBInstanceStorage                                         int
	DeletionProtection                                        *bool
}

func (c *Client) prepareAlibabaDatabaseRecovery(ctx context.Context, region string, spec provider.DatabaseRecovery, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if allowed, ok := c.profile.AlibabaDatabaseRecoveryPlacements[region]; !ok || allowed != spec.DatabaseRecoveryPlacement ||
		!launchTokenPattern.MatchString(token) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	source, err := c.readAlibabaRecoveryInstance(ctx, region, spec.SourceID)
	if err != nil {
		return nil, err
	}
	if !source.basicMySQL() || source.DBInstanceStatus != "Running" || source.InstanceNetworkType != "VPC" ||
		!strings.HasPrefix(source.VpcId, "vpc-") || source.VpcId == spec.VPC || source.DBInstanceDescription == spec.Name ||
		source.DBInstanceClass != spec.Flavor || source.DBInstanceStorage <= 0 || source.DBInstanceStorage > spec.StorageGiB {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	windows, err := c.alibabaRestoreWindow(ctx, region, spec.SourceID)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseRestoreWindows(windows); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(windows, func(window provider.DatabaseRestoreWindow) bool { return window.Contains(spec.RestoreTime) }) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	if _, err := c.readAlibabaRecoveryProtection(ctx, region, spec.SourceID); err != nil {
		return nil, err
	}
	params := url.Values{
		"ClientToken": {token}, "RegionId": {region}, "DBInstanceId": {spec.SourceID},
		"DBInstanceDescription": {spec.Name}, "RestoreTime": {spec.RestoreTime.UTC().Format(time.RFC3339)},
		"InstanceNetworkType": {"VPC"}, "VPCId": {spec.VPC}, "VSwitchId": {spec.Subnet}, "ZoneId": {spec.Zone},
		"PayType": {"Postpaid"}, "Category": {"Basic"}, "DBInstanceClass": {spec.Flavor},
		"DBInstanceStorageType": {spec.StorageType}, "DBInstanceStorage": {strconv.Itoa(spec.StorageGiB)}, "DeletionProtection": {"true"},
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		var result struct{ DBInstanceId, RequestId string }
		if err := c.alibabaRecoveryRPC(ctx, region, "CloneDBInstance", params, &result); err != nil {
			return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		if !resourceID.MatchString(result.RequestId) || !provider.ValidDatabaseRecoveryInstanceID(c.name, result.DBInstanceId) ||
			result.DBInstanceId == spec.SourceID {
			return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		return provider.LaunchReceipt{RequestID: result.RequestId, ResourceID: result.DBInstanceId}, nil
	}, nil
}

func (c *Client) observeAlibabaDatabaseRecovery(ctx context.Context, region string, spec provider.DatabaseRecovery, id string) (provider.Object, error) {
	target, err := c.readAlibabaRecoveryInstance(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	if target.DBInstanceStatus != "Running" {
		return provider.Object{}, nil
	}
	if !target.basicMySQL() || target.DBInstanceDescription != spec.Name ||
		target.PayType != "Postpaid" || target.InstanceNetworkType != "VPC" || target.VpcId != spec.VPC ||
		target.VSwitchId != spec.Subnet || target.ZoneId != spec.Zone ||
		target.DBInstanceClass != spec.Flavor || target.DBInstanceStorage != spec.StorageGiB ||
		target.DeletionProtection == nil || !*target.DeletionProtection {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	source, err := c.readAlibabaRecoveryInstance(ctx, region, spec.SourceID)
	if err != nil {
		return provider.Object{}, err
	}
	if !source.basicMySQL() || source.EngineVersion != target.EngineVersion {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	targetProtection, err := c.readAlibabaRecoveryProtection(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	sourceProtection, err := c.readAlibabaRecoveryProtection(ctx, region, spec.SourceID)
	if err != nil {
		return provider.Object{}, err
	}
	if targetProtection.diskKey != sourceProtection.diskKey || !slices.Equal(targetProtection.whitelist, sourceProtection.whitelist) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return databaseObject(id, target.DBInstanceDescription, target.DBInstanceStatus, provider.AlibabaRDS, target.Engine, target.EngineVersion, target.VpcId)
}

func (row alibabaRecoveryInstance) basicMySQL() bool {
	return strings.EqualFold(row.Engine, "mysql") && row.EngineVersion != "" && row.Category == "Basic" &&
		row.DBInstanceType == "Primary" && row.DBInstanceStorageType == "cloud_essd"
}

func (c *Client) readAlibabaRecoveryInstance(ctx context.Context, region, id string) (alibabaRecoveryInstance, error) {
	var result struct {
		Items struct{ DBInstanceAttribute []alibabaRecoveryInstance }
	}
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeDBInstanceAttribute", url.Values{"DBInstanceId": {id}}, &result); err != nil {
		return alibabaRecoveryInstance{}, err
	}
	rows := result.Items.DBInstanceAttribute
	if rows != nil && len(rows) == 0 {
		return alibabaRecoveryInstance{}, &provider.Failure{Kind: provider.Absent}
	}
	if len(rows) != 1 || rows[0].DBInstanceId != id || rows[0].RegionId != region || rows[0].DBInstanceStatus == "" {
		return alibabaRecoveryInstance{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return rows[0], nil
}

func (c *Client) alibabaRecoveryRPC(ctx context.Context, region, action string, params url.Values, dst any) error {
	var raw json.RawMessage
	if err := c.alibaba(ctx, alibabaRDSEndpoint(region), action, "2014-08-15", params, &raw); err != nil {
		return err
	}
	var envelope struct{ Code string }
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if envelope.Code != "" {
		return cloudFailure(envelope.Code)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}
