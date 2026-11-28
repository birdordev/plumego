package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ReadDatabaseRestoreWindows(ctx context.Context, scope provider.Scope, id string, db provider.DatabaseDetails) ([]provider.DatabaseRestoreWindow, error) {
	if scope.Kind != provider.DatabaseInstance || provider.DatabaseRestoreWindowScope(c.name, id, db) == "" {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if err := c.checkDatabaseBackupParent(ctx, scope, id, db); err != nil {
		return nil, err
	}
	var read func(context.Context, string, string) ([]provider.DatabaseRestoreWindow, error)
	switch db.Product {
	case provider.TencentCDB:
		read = c.tencentCDBRestoreWindows
	case provider.TencentPostgreSQL:
		read = c.tencentPostgresRestoreWindow
	case provider.TencentSQLServer:
		read = c.tencentSQLServerRestoreWindow
	case provider.AlibabaRDS:
		read = c.alibabaRestoreWindow
	case provider.HuaweiRDS:
		read = c.huaweiRestoreWindows
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	windows, err := read(ctx, scope.Region, id)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseRestoreWindows(windows); err != nil {
		return nil, err
	}
	if err := c.checkDatabaseBackupParent(ctx, scope, id, db); err != nil {
		return nil, err
	}
	return windows, nil
}

type nativeRecoveryInterval struct{ RecoveryBeginTime, RecoveryEndTime string }

func (c *Client) tencentPostgresRestoreWindow(ctx context.Context, region, id string) ([]provider.DatabaseRestoreWindow, error) {
	var result nativeRecoveryInterval
	if err := c.tencentRestoreWindow(ctx, "postgres", region, "DescribeAvailableRecoveryTime", "2017-03-12", struct{ DBInstanceId string }{id}, &result); err != nil {
		return nil, err
	}
	window, err := decodeRestoreWindow(result.RecoveryBeginTime, result.RecoveryEndTime, databaseBackupChinaZone)
	return []provider.DatabaseRestoreWindow{window}, err
}

func (c *Client) tencentSQLServerRestoreWindow(ctx context.Context, region, id string) ([]provider.DatabaseRestoreWindow, error) {
	zone, err := c.databaseBackupTimeZone(id)
	if err != nil {
		return nil, err
	}
	var result struct{ MinTime, MaxTime string }
	if err := c.tencentRestoreWindow(ctx, "sqlserver", region, "DescribeRestoreTimeRange", "2018-03-28", struct{ InstanceId string }{id}, &result); err != nil {
		return nil, err
	}
	window, err := decodeRestoreWindow(result.MinTime, result.MaxTime, zone)
	return []provider.DatabaseRestoreWindow{window}, err
}

func (c *Client) tencentCDBRestoreWindows(ctx context.Context, region, id string) ([]provider.DatabaseRestoreWindow, error) {
	zone, err := c.databaseBackupTimeZone(id)
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount *int
		Items      []struct {
			InstanceId string
			Code       *int
			Times      []struct{ Begin, End string }
		}
	}
	if err := c.tencentRestoreWindow(ctx, "cdb", region, "DescribeRollbackRangeTime", "2017-03-20", struct{ InstanceIds []string }{[]string{id}}, &result); err != nil {
		return nil, err
	}
	if result.TotalCount == nil || *result.TotalCount != 1 || len(result.Items) != 1 {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	item := result.Items[0]
	if item.InstanceId != id || item.Code == nil || *item.Code != 0 || item.Times == nil || len(item.Times) > provider.MaxDatabaseRestoreWindows {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	windows := make([]provider.DatabaseRestoreWindow, len(item.Times))
	for i, interval := range item.Times {
		window, err := decodeRestoreWindow(interval.Begin, interval.End, zone)
		if err != nil {
			return nil, err
		}
		windows[i] = window
	}
	return windows, nil
}

func (c *Client) tencentRestoreWindow(ctx context.Context, service, region, action, version string, input, result any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return c.tencent(ctx, service, region, action, version, body, result)
}

func (c *Client) alibabaRestoreWindow(ctx context.Context, region, id string) ([]provider.DatabaseRestoreWindow, error) {
	params := url.Values{"DBInstanceId": {id}, "Region": {region}}
	var result struct {
		DBInstanceId string
		nativeRecoveryInterval
	}
	if err := c.alibaba(ctx, alibabaRDSEndpoint(region), "DescribeLocalAvailableRecoveryTime", "2014-08-15", params, &result); err != nil {
		return nil, err
	}
	start, startErr := time.Parse(time.RFC3339Nano, result.RecoveryBeginTime)
	end, endErr := time.Parse(time.RFC3339Nano, result.RecoveryEndTime)
	if result.DBInstanceId != id || startErr != nil || endErr != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return []provider.DatabaseRestoreWindow{{Start: start, End: end}}, nil
}

func (c *Client) huaweiRestoreWindows(ctx context.Context, region, id string) ([]provider.DatabaseRestoreWindow, error) {
	endpoint := url.URL{Scheme: "https", Host: "rds." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/instances/" + id + "/restore-time"}
	var result struct {
		Windows []struct {
			Start int64 `json:"start_time"`
			End   int64 `json:"end_time"`
		} `json:"restore_time"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	if result.Windows == nil || len(result.Windows) > provider.MaxDatabaseRestoreWindows {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	windows := make([]provider.DatabaseRestoreWindow, len(result.Windows))
	for i, interval := range result.Windows {
		if interval.Start <= 0 || interval.End <= 0 {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		windows[i] = provider.DatabaseRestoreWindow{Start: time.UnixMilli(interval.Start), End: time.UnixMilli(interval.End)}
	}
	return windows, nil
}

func decodeRestoreWindow(begin, end string, zone *time.Location) (provider.DatabaseRestoreWindow, error) {
	start, err := backupTime(begin, zone)
	if err != nil {
		return provider.DatabaseRestoreWindow{}, err
	}
	finish, err := backupTime(end, zone)
	return provider.DatabaseRestoreWindow{Start: start, End: finish}, err
}
