package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/billing"
)

const allocationPreviewRoute = allocationRoute + "/preview"

type allocationWriteTarget struct {
	key                        billing.Key
	revision                   int64
	reportDigest, policyDigest string
}

func allocationWriteQuery(tenant, project, raw string, preview bool) (allocationWriteTarget, error) {
	if len(raw) > 1024 {
		return allocationWriteTarget{}, billing.ErrInvalid
	}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return allocationWriteTarget{}, billing.ErrInvalid
	}
	var target allocationWriteTarget
	if !preview {
		report, policy := params["report_sha256"], params["policy_sha256"]
		if len(report) != 1 || len(policy) != 1 || len(report[0]) != 64 || len(policy[0]) != 64 {
			return allocationWriteTarget{}, billing.ErrInvalid
		}
		target.reportDigest, target.policyDigest = report[0], policy[0]
		params.Del("report_sha256")
		params.Del("policy_sha256")
	}
	target.key, target.revision, err = billingQuery(tenant, project, params.Encode())
	if err != nil || target.revision == 0 {
		return allocationWriteTarget{}, billing.ErrInvalid
	}
	return target, nil
}

func allocationMatch(r *http.Request, preview bool) (int64, error) {
	headers := r.Header.Values("If-Match")
	if preview {
		if len(headers) == 0 {
			return 0, nil
		}
		return 0, billing.ErrInvalid
	}
	if len(headers) == 1 {
		if text, err := strconv.Unquote(headers[0]); err == nil && strconv.Quote(text) == headers[0] {
			if version, valid := billingRevision(text, true); valid {
				return version, nil
			}
		}
	}
	return 0, billing.ErrAllocationConflict
}

func allocationWriteOpenAPI(auth []openapi.SecurityRequirement, preview bool) openapi.Op {
	op := allocationOpenAPI(auth)
	op.Params = op.Params[:4]
	op.Params[3].Required = true
	op.Params[3].Description = "精确账单版本"
	op.Body = &openapi.RequestBody{Required: true, Description: "费用分摊规则 rules 和默认 shares。权重以基点计。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object", Properties: map[string]openapi.Schema{
				"rules": {Type: "array", Items: &openapi.Schema{Type: "object"}},
				"default": {Type: "array", Items: &openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
					"center": openapi.String, "weight": openapi.Integer,
				}}},
			},
		}}},
	}
	op.Responses["413"] = openapi.Response{Description: "规则超过正文上限"}
	op.Responses["415"] = openapi.Response{Description: "需要 JSON 请求"}
	if preview {
		op.OperationID, op.Summary = "previewBillingAllocation", "预览指定账单版本的费用分摊"
		op.Responses["200"] = openapi.Response{Description: "原币种预览、账单摘要、规则摘要和当前分摊版本"}
		return op
	}
	op.OperationID, op.Summary = "publishBillingAllocation", "发布经预览的原币种费用分摊"
	op.Params = append(op.Params,
		openapi.Param{Name: "report_sha256", In: "query", Required: true, Schema: openapi.String, Description: "预览返回的原账单摘要"},
		openapi.Param{Name: "policy_sha256", In: "query", Required: true, Schema: openapi.String, Description: "预览返回的规则摘要"},
		openapi.Param{Name: "If-Match", In: "header", Required: true, Schema: openapi.String, Description: "当前分摊版本，首次发布使用带引号的 0"})
	op.Responses["200"] = openapi.Response{Description: "已发布的版本或相同规则的原收据"}
	op.Responses["412"] = openapi.Response{Description: "账单摘要、规则摘要或分摊版本已变化"}
	return op
}
