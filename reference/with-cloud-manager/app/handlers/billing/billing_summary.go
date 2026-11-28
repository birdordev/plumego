package billinghttp

import (
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/billing"
)

const BillingSummaryRoute = "/v1/tenants/:tenant/projects/:project/billing/summary"

func BillingSummaryQuery(tenant, project, raw string) (billing.ProjectSummaryQuery, error) {
	if len(raw) > 2048 {
		return billing.ProjectSummaryQuery{}, billing.ErrInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return billing.ProjectSummaryQuery{}, billing.ErrInvalid
	}
	q := billing.ProjectSummaryQuery{Tenant: tenant, Project: project, Limit: 50}
	for key, entries := range values {
		if len(entries) != 1 || entries[0] == "" {
			return billing.ProjectSummaryQuery{}, billing.ErrInvalid
		}
		value := entries[0]
		switch key {
		case "from":
			q.From = value
		case "through":
			q.Through = value
		case "source":
			q.Source = value
		case "currency":
			q.Currency = value
		case "provider":
			q.Provider = value
		case "after":
			q.After = value
		case "missing_only":
			if value != "true" && value != "false" {
				return billing.ProjectSummaryQuery{}, billing.ErrInvalid
			}
			q.MissingOnly = value == "true"
		case "limit":
			q.Limit, err = strconv.Atoi(value)
			if err != nil || strconv.Itoa(q.Limit) != value {
				return billing.ProjectSummaryQuery{}, billing.ErrInvalid
			}
		default:
			return billing.ProjectSummaryQuery{}, billing.ErrInvalid
		}
	}
	if !q.Valid() {
		return billing.ProjectSummaryQuery{}, billing.ErrInvalid
	}
	return q, nil
}

func BillingSummaryOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	month := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"period": openapi.String, "eligible_accounts": openapi.Integer, "published_accounts": openapi.Integer, "currency_accounts": openapi.Integer,
		"total": {Type: "object", Properties: map[string]openapi.Schema{
			"currency": openapi.String, "billed_cost": openapi.String, "effective_cost": openapi.String, "list_cost": openapi.String,
			"expected_billed_cost": openapi.String, "variance": openapi.String,
		}},
	}}
	account := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"period": openapi.String, "account": openapi.String, "provider": openapi.String, "billed_cost": openapi.String,
		"report": {Type: "object", Properties: map[string]openapi.Schema{
			"revision": openapi.String, "sha256": openapi.String, "rows": openapi.Integer,
		}},
	}}
	schema := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"data": {Type: "object", Properties: map[string]openapi.Schema{
			"from": openapi.String, "through": openapi.String, "source": openapi.String, "currency": openapi.String, "provider": openapi.String,
			"as_of": {Type: "string", Format: "date-time"}, "next": openapi.String,
			"months": {Type: "array", Items: &month}, "accounts": {Type: "array", Items: &account},
		}},
	}}
	return openapi.Op{
		OperationID: "getProjectBillingSummary",
		Summary:     "按原币种汇总项目自有的可读云账号，列出账单覆盖与纳入的最新版本",
		Security:    readAuth,
		Params: []openapi.Param{
			{Name: "from", In: "query", Required: true, Schema: openapi.String, Description: "起始账期 YYYY-MM"},
			{Name: "through", In: "query", Required: true, Schema: openapi.String, Description: "最后账期 YYYY-MM，包含该月，最多 12 个月"},
			{Name: "source", In: "query", Required: true, Schema: openapi.String, Description: "native 或 focus-1.0"},
			{Name: "currency", In: "query", Required: true, Schema: openapi.String, Description: "三位大写原币种，不进行换算"},
			{Name: "provider", In: "query", Schema: openapi.String, Description: "可选 tencent-cvm、alibaba-ecs 或 huawei-ecs"},
			{Name: "missing_only", In: "query", Schema: openapi.Boolean, Description: "只筛选未发布所选来源的明细，月度汇总保持完整"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "明细页大小 1 至 100，默认 50"},
			{Name: "after", In: "query", Schema: openapi.String, Description: "沿用筛选条件并传入上一页的 next"},
		},
		Responses: map[string]openapi.Response{
			"200": {Description: "同一快照下的月度费用、覆盖与分页账单版本。费用和来源版本均为十进制字符串。",
				Content: map[string]openapi.MediaType{"application/json": {Schema: schema}}},
			"400": {Description: "查询参数或账期范围无效"},
			"401": {Description: "凭据缺失、过期或无效"},
			"403": {Description: "当前身份没有项目账单管理权限"},
			"503": {Description: "账单存储不可用"},
		},
	}
}
