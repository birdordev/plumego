package governance

import (
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/inventory"
)

const DatabaseProtectionRoute = transport.GovernanceRoute + "/database-protection"

func databaseProtectionQuery(r *http.Request) (inventory.DatabaseProtectionQuery, error) {
	q := inventory.DatabaseProtectionQuery{
		Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"),
		Window: inventory.DefaultDatabaseProtectionWindow, MaxAge: inventory.DefaultDatabaseProtectionWindow,
		Limit: inventory.DefaultDatabaseProtectionLimit,
	}
	if len(r.URL.RawQuery) > 8192 {
		return q, inventory.ErrInvalidQuery
	}
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, inventory.ErrInvalidQuery
	}
	for name, values := range params {
		if len(values) != 1 || values[0] == "" {
			return q, inventory.ErrInvalidQuery
		}
		switch value := values[0]; name {
		case "provider":
			q.Provider = value
		case "account":
			q.Account = value
		case "region":
			q.Region = value
		case "finding":
			q.Finding = value
		case "after":
			q.After = value
		case "window_seconds":
			q.Window, err = strconv.Atoi(value)
		case "max_age_seconds":
			q.MaxAge, err = strconv.Atoi(value)
		case "limit":
			q.Limit, err = strconv.Atoi(value)
		default:
			return q, inventory.ErrInvalidQuery
		}
		if err != nil {
			return q, inventory.ErrInvalidQuery
		}
	}
	return q, nil
}

func DatabaseProtectionOptions(args []string) (url.Values, bool) {
	flags := flag.NewFlagSet("database-protection", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cloud := flags.String("provider", "", "")
	account := flags.String("account", "", "")
	region := flags.String("region", "", "")
	finding := flags.String("finding", "", "")
	after := flags.String("after", "", "")
	window := flags.Int("window-seconds", inventory.DefaultDatabaseProtectionWindow, "")
	maxAge := flags.Int("max-age-seconds", inventory.DefaultDatabaseProtectionWindow, "")
	limit := flags.Int("limit", inventory.DefaultDatabaseProtectionLimit, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 ||
		*window < 60 || *window > 604800 || *maxAge < 60 || *maxAge > 2592000 {
		return nil, false
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "window_seconds": {strconv.Itoa(*window)}, "max_age_seconds": {strconv.Itoa(*maxAge)}}
	for key, value := range map[string]string{
		"provider": *cloud, "account": *account, "region": *region, "finding": *finding, "after": *after,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query, true
}

func DatabaseProtectionOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	stamp := openapi.Schema{Type: "string", Format: "date-time"}
	evidence := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"status":      openapi.String,
		"observed_at": stamp, "latest_evidence_at": stamp, "scope": openapi.String,
		"schedule_enabled": openapi.Boolean, "last_refresh_outcome": openapi.String, "last_refresh_at": stamp,
	}}
	item := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"id": openapi.String, "name": openapi.String, "account": openapi.String, "region": openapi.String,
		"provider": openapi.String, "product": openapi.String, "engine": openapi.String, "inventory_available": openapi.Boolean,
		"backups": evidence, "restore_windows": evidence,
	}}
	counts := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"ready": openapi.Integer, "outdated": openapi.Integer, "missing": openapi.Integer,
		"stale": openapi.Integer, "unobserved": openapi.Integer, "unsupported": openapi.Integer,
	}}
	report := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"as_of": stamp, "window_seconds": openapi.Integer, "max_age_seconds": openapi.Integer, "next": openapi.String,
		"items": {Type: "array", Items: &item},
		"summary": {Type: "object", Properties: map[string]openapi.Schema{
			"total": openapi.Integer, "inventory_unavailable": openapi.Integer, "refresh_failed": openapi.Integer,
			"backups": counts, "restore_windows": counts,
		}},
	}}
	return openapi.Op{OperationID: "listDatabaseProtection", Security: readAuth, Summary: "核对数据库备份和时间点恢复保障", Params: []openapi.Param{
		{Name: "provider", In: "query", Schema: openapi.String},
		{Name: "account", In: "query", Schema: openapi.String},
		{Name: "region", In: "query", Schema: openapi.String},
		{Name: "finding", In: "query", Schema: openapi.String, Description: "仅筛选明细。支持 inventory_unavailable、backup_attention、recovery_attention、refresh_failed。"},
		{Name: "window_seconds", In: "query", Schema: openapi.Integer, Description: "观测有效期，60 至 604800 秒，默认 86400 秒。"},
		{Name: "max_age_seconds", In: "query", Schema: openapi.Integer, Description: "备份起始时间或恢复窗口结束时间的最大年龄，60 至 2592000 秒，默认 86400 秒。"},
		{Name: "after", In: "query", Schema: openapi.String, Description: "保留筛选项并传入上一页的 next。"},
		{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
	}, Responses: map[string]openapi.Response{
		"200": {Description: "同一数据库快照中的项目汇总和实例明细。汇总独立于问题筛选和游标。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
				Type: "object", Properties: map[string]openapi.Schema{"data": report},
			}}}},
		"400": {Description: "筛选项或游标无效"}, "401": {Description: "凭据缺失、过期或无效"},
		"403": {Description: "项目权限不足"}, "503": {Description: "资产存储不可用"},
	}}
}
