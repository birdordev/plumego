package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
)

const billingRoute = "/v1/tenants/:tenant/projects/:project/billing/reports"

func billingHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		key, revision, err := billingQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		report, err := store.Get(ctx, token, key, revision)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		revisionText := strconv.FormatInt(report.Revision, 10)
		w.Header().Set("ETag", strconv.Quote(revisionText))
		if err := contract.WriteResponse(w, r, http.StatusOK, struct {
			billing.Report
			RevisionText string `json:"revision_text"`
		}{Report: report, RevisionText: revisionText}, nil); err != nil {
			logger.Warn("billing report response write failed", plumelog.Fields{})
		}
	})
}

func billingSummaryHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := billingSummaryQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		summary, err := store.ReadProjectSummary(ctx, token, query)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, summary, nil); err != nil {
			logger.Warn("billing summary response write failed", plumelog.Fields{})
		}
	})
}

func billingQuery(tenant, project, raw string) (billing.Key, int64, error) {
	if len(raw) > 1024 {
		return billing.Key{}, 0, billing.ErrInvalid
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return billing.Key{}, 0, billing.ErrInvalid
	}
	var revision int64
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return billing.Key{}, 0, billing.ErrInvalid
		}
		switch key {
		case "account", "period", "source":
		case "revision":
			var ok bool
			revision, ok = billingRevision(values[0], false)
			if !ok {
				return billing.Key{}, 0, billing.ErrInvalid
			}
		default:
			return billing.Key{}, 0, billing.ErrInvalid
		}
	}
	key := billing.Key{Tenant: tenant, Project: project, Account: query.Get("account"), Period: query.Get("period"), Source: query.Get("source")}
	if !key.Valid() {
		return billing.Key{}, 0, billing.ErrInvalid
	}
	return key, revision, nil
}

func writeBillingError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	if _, ok := contract.AsAPIError(err); ok {
		writeAccountError(w, r, err, logger)
		return
	}
	switch {
	case errors.Is(err, billing.ErrInvalid):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid billing query or configuration").Build()
	case errors.Is(err, billing.ErrNotFound):
		err = contract.NewErrorBuilder().Type(contract.TypeNotFound).Message("billing record not found").Build()
	case errors.Is(err, billing.ErrBudgetExists):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("billing budget already exists").Build()
	case errors.Is(err, billing.ErrBudgetStale):
		err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current budget ETag required").Build()
	case errors.Is(err, identity.ErrUnauthenticated), errors.Is(err, identity.ErrForbidden):
	default:
		err = contract.NewErrorBuilder().Type(contract.TypeUnavailable).Message("billing report is unavailable").Build()
	}
	writeAccountError(w, r, err, logger)
}

func billingOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	return openapi.Op{
		OperationID: "getBillingReport",
		Summary:     "读取归属项目的账单版本及原币种费用和对账差额",
		Security:    readAuth,
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "period", In: "query", Required: true, Schema: openapi.String, Description: "YYYY-MM"},
			{Name: "source", In: "query", Required: true, Schema: openapi.String, Description: "native 或 focus-1.0"},
			{Name: "revision", In: "query", Schema: openapi.Integer, Description: "指定历史版本，省略时读取当前版本"},
		},
		Responses: map[string]openapi.Response{
			"200": {Description: "当期或历史账单汇总、费用十进制字符串、原文件摘要和对账差额。revision_text 为与 ETag 一致的精确版本字符串。"},
			"400": {Description: "查询参数无效"},
			"401": {Description: "凭据缺失、过期或无效"},
			"403": {Description: "当前身份没有归属项目的账号管理权限"},
			"404": {Description: "账单或指定版本不存在"},
			"503": {Description: "账单存储不可用"},
		},
	}
}
