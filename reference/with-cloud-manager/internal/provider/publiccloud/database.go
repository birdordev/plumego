package publiccloud

import (
	"context"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func databaseObject(id, name, status, product, engine, version, network string) (provider.Object, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "sql server" {
		engine = "sqlserver"
	}
	item := provider.Object{
		ID: id, Name: name, Status: status,
		Database: &provider.DatabaseDetails{Product: product, Engine: engine, Version: version},
	}
	item.Link(provider.Network, network, "in_network")
	if !resourceID.MatchString(id) || (network != "" && !resourceID.MatchString(network)) || !item.ValidDatabase() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func (c *Client) databaseReaders() []regionalInstanceReader {
	switch c.name {
	case provider.TencentCVM:
		return []regionalInstanceReader{c.tencentMySQL, c.tencentPostgreSQL, c.tencentSQLServer}
	case provider.AlibabaECS:
		return []regionalInstanceReader{c.alibabaRDS}
	case provider.HuaweiECS:
		return []regionalInstanceReader{c.huaweiRDS}
	default:
		return nil
	}
}

func (c *Client) discoverDatabases(ctx context.Context, region, cursor string) (provider.Page, error) {
	return discoverRegionalInstances(ctx, region, cursor, regionalInstancePageSize, c.databaseReaders())
}

func (c *Client) observeDatabase(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !slices.Contains(c.profile.Regions, scope.Region) || !resourceID.MatchString(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	readers := c.databaseReaders()
	if len(readers) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	product := 0
	if c.name == provider.TencentCVM {
		switch {
		case strings.HasPrefix(id, "cdb-"), strings.HasPrefix(id, "cdbro-"), strings.HasPrefix(id, "cdbdr-"):
		case strings.HasPrefix(id, "postgres-"):
			product = 1
		case strings.HasPrefix(id, "mssql-"), strings.HasPrefix(id, "mssqldr-"):
			product = 2
		default:
			return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
		}
	}
	return observeRegionalInstance(ctx, scope.Region, id, readers[product])
}
