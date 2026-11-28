package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"with-cloud-manager/internal/provider"
)

func projectDatabaseBackups[T any](rows []T, total *int, project func(T) (provider.DatabaseBackup, error)) (databaseBackupPage, error) {
	if rows == nil {
		return databaseBackupPage{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := databaseBackupPage{total: total, backups: make([]provider.DatabaseBackup, 0, len(rows))}
	for _, row := range rows {
		backup, err := project(row)
		if err != nil {
			return databaseBackupPage{}, err
		}
		page.backups = append(page.backups, backup)
	}
	return page, nil
}

func (c *Client) tencentBackupPage(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	switch query.details.Product {
	case provider.TencentCDB:
		return c.tencentCDBBackups(ctx, query, offset)
	case provider.TencentPostgreSQL:
		return c.tencentPostgresBackups(ctx, query, offset)
	case provider.TencentSQLServer:
		return c.tencentSQLServerBackups(ctx, query, offset)
	default:
		return databaseBackupPage{}, &provider.Failure{Kind: provider.Unsupported}
	}
}

type cdbBackup struct {
	BackupId              uint64
	Status, Method, Way   string
	StartTime, FinishTime string
	Size                  *int64
}

func (c *Client) tencentCDBBackups(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	body, err := json.Marshal(struct {
		InstanceId, MinStartTime, MaxStartTime string
		Offset, Limit                          int
	}{query.id, query.start.In(query.zone).Format(time.DateTime), query.end.In(query.zone).Format(time.DateTime), offset, databaseBackupPageSize})
	if err != nil {
		return databaseBackupPage{}, err
	}
	var result struct {
		Items      []cdbBackup
		TotalCount *int
	}
	if err := c.tencent(ctx, "cdb", query.region, "DescribeBackups", "2017-03-20", body, &result); err != nil {
		return databaseBackupPage{}, err
	}
	return projectDatabaseBackups(result.Items, result.TotalCount, func(row cdbBackup) (provider.DatabaseBackup, error) {
		return makeDatabaseBackup(decimalBackupID(row.BackupId), backupKind(row.Method), backupMode(row.Way), backupStatus(row.Status),
			row.StartTime, row.FinishTime, row.Size, query.zone)
	})
}

type postgresBackup struct {
	Id, DBInstanceId, BackupMode, State, StartTime, FinishTime string
	Size                                                       *int64
}

func (c *Client) tencentPostgresBackups(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	type filter struct {
		Name   string
		Values []string
	}
	body, err := json.Marshal(struct {
		MinFinishTime, MaxFinishTime string
		Offset, Limit                int
		Filters                      []filter
	}{query.start.In(query.zone).Format(time.DateTime), query.end.In(query.zone).Format(time.DateTime),
		offset, databaseBackupPageSize, []filter{{Name: "db-instance-id", Values: []string{query.id}}}})
	if err != nil {
		return databaseBackupPage{}, err
	}
	var result struct {
		BaseBackupSet []postgresBackup
		TotalCount    *int
	}
	if err := c.tencent(ctx, "postgres", query.region, "DescribeBaseBackups", "2017-03-12", body, &result); err != nil {
		return databaseBackupPage{}, err
	}
	return projectDatabaseBackups(result.BaseBackupSet, result.TotalCount, func(row postgresBackup) (provider.DatabaseBackup, error) {
		if row.DBInstanceId != query.id {
			return provider.DatabaseBackup{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return makeDatabaseBackup(row.Id, "full", backupMode(row.BackupMode), backupStatus(row.State),
			row.StartTime, row.FinishTime, row.Size, query.zone)
	})
}

type sqlServerBackup struct {
	Id                          uint64
	GroupId, StartTime, EndTime string
	Status, BackupWay           *int
	Size                        *int64
}

func (c *Client) tencentSQLServerBackups(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	body, err := json.Marshal(struct {
		InstanceId, StartTime, EndTime string
		Offset, Limit, Group, Type     int
	}{query.id, query.start.In(query.zone).Format(time.DateTime), query.end.In(query.zone).Format(time.DateTime),
		offset / databaseBackupPageSize, databaseBackupPageSize, 1, 1})
	if err != nil {
		return databaseBackupPage{}, err
	}
	var result struct {
		Backups    []sqlServerBackup
		TotalCount *int
	}
	if err := c.tencent(ctx, "sqlserver", query.region, "DescribeBackups", "2018-03-28", body, &result); err != nil {
		return databaseBackupPage{}, err
	}
	return projectDatabaseBackups(result.Backups, result.TotalCount, func(row sqlServerBackup) (provider.DatabaseBackup, error) {
		id := decimalBackupID(row.Id)
		if id == "" && resourceID.MatchString(row.GroupId) {
			id = "group-" + row.GroupId
		}
		status, mode := "unknown", "unknown"
		if row.Status != nil {
			status = backupStatus(strconv.Itoa(*row.Status))
		}
		if row.BackupWay != nil {
			mode = backupMode(strconv.Itoa(*row.BackupWay))
		}
		bytes, err := backupBytesFromKiB(row.Size)
		if err != nil {
			return provider.DatabaseBackup{}, err
		}
		return makeDatabaseBackup(id, "full", mode, status, row.StartTime, row.EndTime, bytes, query.zone)
	})
}
