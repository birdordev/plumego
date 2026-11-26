package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareDatabaseRename(ctx context.Context, scope provider.Scope, change provider.DatabaseRename) (func(context.Context) (string, error), error) {
	if !c.profile.AllowDatabaseRename {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.readDatabaseRenameTarget(ctx, scope, change)
	if provider.IsFailure(err, provider.Absent) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	if item.Name != change.CurrentName || !databaseReady(change.Product, item.Status) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.renameTencentDatabase(ctx, scope.Region, change)
		case provider.AlibabaECS:
			return c.renameAlibabaDatabase(ctx, scope.Region, change)
		case provider.HuaweiECS:
			return c.renameHuaweiDatabase(ctx, scope.Region, change)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ObserveDatabaseRename(ctx context.Context, scope provider.Scope, change provider.DatabaseRename) (provider.ActionResult, error) {
	item, err := c.readDatabaseRenameTarget(ctx, scope, change)
	if err != nil {
		return "", err
	}
	if item.Name == change.Name && databaseReady(change.Product, item.Status) {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) readDatabaseRenameTarget(ctx context.Context, scope provider.Scope, change provider.DatabaseRename) (provider.Object, error) {
	if scope.Kind != provider.DatabaseInstance || !change.ValidFor(c.name) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.observeDatabase(ctx, scope, change.ID)
	if err != nil {
		return provider.Object{}, err
	}
	if item.ID != change.ID || item.Database == nil || item.Database.Product != change.Product {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func databaseReady(product, status string) bool {
	switch product {
	case provider.TencentCDB:
		return status == "1"
	case provider.TencentSQLServer:
		return status == "2"
	case provider.TencentPostgreSQL, provider.AlibabaRDS:
		return strings.EqualFold(status, "running")
	case provider.HuaweiRDS:
		return strings.EqualFold(status, "active")
	default:
		return false
	}
}

func (c *Client) renameTencentDatabase(ctx context.Context, region string, change provider.DatabaseRename) (string, error) {
	service, version, idField := "cdb", "2017-03-20", "InstanceId"
	switch change.Product {
	case provider.TencentPostgreSQL:
		service, version, idField = "postgres", "2017-03-12", "DBInstanceId"
	case provider.TencentSQLServer:
		service, version = "sqlserver", "2018-03-28"
	}
	body, err := json.Marshal(map[string]string{idField: change.ID, "InstanceName": change.Name})
	if err != nil {
		return "", err
	}
	var result struct{ RequestId string }
	if err := c.tencent(ctx, service, region, "ModifyDBInstanceName", version, body, &result); err != nil {
		return "", err
	}
	if !resourceID.MatchString(result.RequestId) {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return result.RequestId, nil
}

func (c *Client) renameAlibabaDatabase(ctx context.Context, region string, change provider.DatabaseRename) (string, error) {
	fields := url.Values{"DBInstanceId": {change.ID}, "DBInstanceDescription": {change.Name}}
	var result struct{ Code, RequestId string }
	if err := c.alibaba(ctx, alibabaRDSEndpoint(region), "ModifyDBInstanceDescription", "2014-08-15", fields, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	if !resourceID.MatchString(result.RequestId) {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return result.RequestId, nil
}
