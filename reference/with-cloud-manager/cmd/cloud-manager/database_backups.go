package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func databaseBackupsQuery(r *http.Request) (inventory.DatabaseBackupQuery, error) {
	query := inventory.DatabaseBackupQuery{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), ID: router.Param(r, "asset"), Limit: 50}
	if len(r.URL.RawQuery) > 2048 {
		return query, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || values.Get("account") == "" || values.Get("region") == "" {
		return query, inventory.ErrInvalidQuery
	}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return query, inventory.ErrInvalidQuery
		}
		switch key {
		case "account":
			query.Account = entries[0]
		case "region":
			query.Region = entries[0]
		case "observed_at":
			query.ObservedAt, err = time.Parse(time.RFC3339Nano, entries[0])
		case "offset":
			query.Offset, err = strconv.Atoi(entries[0])
		case "limit":
			query.Limit, err = strconv.Atoi(entries[0])
		default:
			return query, inventory.ErrInvalidQuery
		}
		if err != nil {
			return query, inventory.ErrInvalidQuery
		}
	}
	return query, nil
}

func runDatabaseBackupsClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(assetsUsage)
	if len(args) < 4 || !operationClientSegment(args[3]) {
		return usage
	}
	flags := flag.NewFlagSet("assets database-backups", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account, region := flags.String("account", "", ""), flags.String("region", "", "")
	limit, offset := flags.Int("limit", 50, ""), flags.Int("offset", 0, "")
	stamp := flags.String("observed-at", "", "")
	if flags.Parse(args[4:]) != nil || flags.NArg() != 0 || !operationClientSegment(*account) || !operationClientSegment(*region) ||
		*limit < 1 || *limit > 100 || *offset < 0 || *offset > provider.MaxDatabaseBackups || (*offset > 0 && *stamp == "") {
		return usage
	}
	params := url.Values{"account": {*account}, "region": {*region}, "offset": {strconv.Itoa(*offset)}, "limit": {strconv.Itoa(*limit)}}
	if *stamp != "" {
		if _, err := time.Parse(time.RFC3339Nano, *stamp); err != nil {
			return usage
		}
		params.Set("observed_at", *stamp)
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "assets", "databases", url.PathEscape(args[3]), "backups")
	target.RawQuery = params.Encode()
	return runProjectRequest(ctx, operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, token, nil, output, transport)
}

func databaseBackupsOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
	return openapi.Op{OperationID: "getDatabaseBackups", Security: auth, Summary: "读取已盘点托管数据库的备份元数据快照",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "region", In: "query", Required: true, Schema: openapi.String},
			{Name: "offset", In: "query", Schema: openapi.Integer, Description: "使用上一页的 next_offset"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最多 100"},
			{Name: "observed_at", In: "query", Schema: openapi.Schema{Type: "string", Format: "date-time"}, Description: "续页使用上一页的观测时间"},
		}, Responses: map[string]openapi.Response{
			"200": {Description: "备份 ID、类型、模式、状态、时间和可选字节数。包含快照采集及查询窗口。"},
			"400": {Description: "作用域、分页或观测时间无效"},
			"401": {Description: "凭据无效"},
			"403": {Description: "项目授权无效"},
			"404": {Description: "资产不可见或没有当前凭据的快照"},
			"409": {Description: "观测快照已变化，须从首页重新查询"},
			"503": {Description: "盘点存储不可用"},
		}}
}
