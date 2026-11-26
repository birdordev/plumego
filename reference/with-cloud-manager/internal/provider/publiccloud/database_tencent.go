package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentDatabasePage(ctx context.Context, service, region, version string, input, result any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return c.tencent(ctx, service, region, "DescribeDBInstances", version, body, result)
}

type tencentMySQLRow struct {
	tencentDBNetwork
	InstanceId, InstanceName, EngineVersion string
}

type tencentDBNetwork struct {
	Region, UniqVpcId string
	Status            *int
}

func (row tencentDBNetwork) asset(region, id, name, product, engine, version string) (provider.Object, error) {
	if row.Region != region || row.Status == nil || *row.Status < 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return databaseObject(id, name, strconv.Itoa(*row.Status), product, engine, version, row.UniqVpcId)
}

func (c *Client) tencentMySQL(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	input := struct {
		Offset, Limit           int
		OrderBy, OrderDirection string
		InstanceIds             []string `json:",omitempty"`
	}{Offset: offset, Limit: regionalInstancePageSize, OrderBy: "instanceId", OrderDirection: "ASC"}
	if id != "" {
		input.InstanceIds = []string{id}
	}
	var result struct {
		TotalCount *int
		Items      []tencentMySQLRow
	}
	if err := c.tencentDatabasePage(ctx, "cdb", region, "2017-03-20", input, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.Items, result.TotalCount, func(row tencentMySQLRow) (provider.Object, error) {
		return row.asset(region, row.InstanceId, row.InstanceName, provider.TencentCDB, "mysql", row.EngineVersion)
	})
}

type tencentPostgreSQLRow struct {
	DBInstanceId, DBInstanceName, DBInstanceStatus, DBEngine, DBVersion, Region, VpcId string
}

func (c *Client) tencentPostgreSQL(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	type filter struct {
		Name   string
		Values []string
	}
	input := struct {
		Offset, Limit        int
		OrderBy, OrderByType string
		Filters              []filter `json:",omitempty"`
	}{Offset: offset, Limit: regionalInstancePageSize, OrderBy: "DBInstanceId", OrderByType: "asc"}
	if id != "" {
		input.Filters = []filter{{Name: "db-instance-id", Values: []string{id}}}
	}
	var result struct {
		TotalCount    *int
		DBInstanceSet []tencentPostgreSQLRow
	}
	if err := c.tencentDatabasePage(ctx, "postgres", region, "2017-03-12", input, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.DBInstanceSet, result.TotalCount, func(row tencentPostgreSQLRow) (provider.Object, error) {
		if row.Region != region {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return databaseObject(row.DBInstanceId, row.DBInstanceName, row.DBInstanceStatus, provider.TencentPostgreSQL, row.DBEngine, row.DBVersion, row.VpcId)
	})
}

type tencentSQLServerRow struct {
	tencentDBNetwork
	InstanceId, Name, Version string
}

func (c *Client) tencentSQLServer(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	input := struct {
		Offset, Limit int
		InstanceIdSet []string `json:",omitempty"`
	}{Offset: offset, Limit: regionalInstancePageSize}
	if id != "" {
		input.InstanceIdSet = []string{id}
	}
	var result struct {
		TotalCount  *int
		DBInstances []tencentSQLServerRow
	}
	if err := c.tencentDatabasePage(ctx, "sqlserver", region, "2018-03-28", input, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.DBInstances, result.TotalCount, func(row tencentSQLServerRow) (provider.Object, error) {
		return row.asset(region, row.InstanceId, row.Name, provider.TencentSQLServer, "sqlserver", row.Version)
	})
}
