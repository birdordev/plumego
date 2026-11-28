package publiccloud

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const databaseBackupPageSize = 100

type databaseBackupQuery struct {
	region, id string
	details    provider.DatabaseDetails
	start, end time.Time
	zone       *time.Location
}

type databaseBackupPage struct {
	backups []provider.DatabaseBackup
	total   *int
}

func (c *Client) ReadDatabaseBackups(ctx context.Context, scope provider.Scope, id string, details provider.DatabaseDetails, at time.Time) ([]provider.DatabaseBackup, error) {
	if scope.Kind != provider.DatabaseInstance || !provider.ValidDatabaseBackupTarget(c.name, id, details) || at.IsZero() {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if err := c.checkDatabaseBackupParent(ctx, scope, id, details); err != nil {
		return nil, err
	}
	zone := databaseBackupChinaZone
	if c.name == provider.TencentCVM {
		if name := c.profile.DatabaseBackupTimeZones[id]; name != "" {
			location, err := time.LoadLocation(name)
			if err != nil || name == "Local" {
				return nil, &provider.Failure{Kind: provider.Unsupported}
			}
			zone = location
		}
	}
	query := databaseBackupQuery{region: scope.Region, id: id, details: details, start: at.Add(-provider.DatabaseBackupWindow), end: at, zone: zone}
	var reader func(context.Context, databaseBackupQuery, int) (databaseBackupPage, error)
	switch c.name {
	case provider.AlibabaECS:
		reader = c.alibabaBackupPage
	case provider.HuaweiECS:
		reader = c.huaweiBackupPage
	default:
		reader = c.tencentBackupPage
	}
	backups := make([]provider.DatabaseBackup, 0)
	expected := -1
	for offset := 0; ; offset += databaseBackupPageSize {
		page, err := reader(ctx, query, offset)
		if err != nil {
			return nil, err
		}
		if page.total == nil || *page.total < offset || *page.total > provider.MaxDatabaseBackups ||
			page.backups == nil || len(page.backups) != min(databaseBackupPageSize, *page.total-offset) ||
			(expected != -1 && *page.total != expected) {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		expected = *page.total
		backups = append(backups, page.backups...)
		if len(backups) == expected {
			break
		}
	}
	if err := c.checkDatabaseBackupParent(ctx, scope, id, details); err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseBackups(backups); err != nil {
		return nil, err
	}
	return backups, nil
}

func (c *Client) checkDatabaseBackupParent(ctx context.Context, scope provider.Scope, id string, details provider.DatabaseDetails) error {
	item, err := c.observeDatabase(ctx, scope, id)
	if err != nil {
		return err
	}
	if item.ID != id || item.Database == nil || item.Database.Product != details.Product || item.Database.Engine != details.Engine {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

// 腾讯云中国站备份接口使用未带时区的时间字段。
var databaseBackupChinaZone = time.FixedZone("UTC+08", 8*60*60)

func backupTime(raw string, zone *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05-0700", time.DateTime} {
		if value, err := time.ParseInLocation(layout, raw, zone); err == nil {
			return value.UTC(), nil
		}
	}
	return time.Time{}, &provider.Failure{Kind: provider.InvalidResponse}
}

func makeDatabaseBackup(id, kind, mode, status, start, end string, size *int64, zone *time.Location) (provider.DatabaseBackup, error) {
	started, err := backupTime(start, zone)
	if err != nil {
		return provider.DatabaseBackup{}, err
	}
	result := provider.DatabaseBackup{ID: id, Kind: kind, Mode: mode, Status: status, StartedAt: started, SizeBytes: size}
	if end != "" {
		finished, err := backupTime(end, zone)
		if err != nil {
			return provider.DatabaseBackup{}, err
		}
		result.EndedAt = &finished
	}
	return result, nil
}

func backupStatus(status string) string {
	switch strings.ToLower(status) {
	case "success", "completed", "finished", "1":
		return "ready"
	case "building", "running", "pending", "creating", "create", "init", "0":
		return "pending"
	case "failed", "failure", "fail", "error", "2":
		return "failed"
	case "deleting":
		return "deleting"
	default:
		return "unknown"
	}
}

func backupKind(kind string) string {
	switch strings.ToLower(kind) {
	case "full", "fullbackup":
		return "full"
	case "increment", "incremental", "incrementalbackup":
		return "incremental"
	case "fragment":
		return "fragment"
	default:
		return "unknown"
	}
}

func backupMode(mode string) string {
	switch strings.ToLower(mode) {
	case "auto", "automatic", "automated", "system", "0", "2":
		return "automatic"
	case "manual", "user", "1":
		return "manual"
	default:
		return "unknown"
	}
}

func decimalBackupID(value uint64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(value, 10)
}

func backupBytesFromKiB(size *int64) (*int64, error) {
	if size == nil {
		return nil, nil
	}
	if *size < 0 || *size > math.MaxInt64/1024 {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	bytes := *size * 1024
	return &bytes, nil
}
