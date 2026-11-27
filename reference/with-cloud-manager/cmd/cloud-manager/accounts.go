package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
)

const accountsRoute = "/v1/tenants/:tenant/projects/:project/cloud-accounts"
const accountGrantsRoute = accountsRoute + "/:account/grants"
const accountSchedulesRoute = accountsRoute + "/:account/inventory-schedules"
const accountScheduleRoute = accountSchedulesRoute + "/:schedule"
const accountInputLimit = 4096

func inventorySchedulesHandler(store controller.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		account := router.Param(r, "account")
		var result any
		var err error
		switch r.Method {
		case http.MethodPost:
			var input controller.RegisterSchedule
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
				result, err = store.RegisterSchedule(ctx, token, scope, account, input)
			}
		case http.MethodPatch:
			result, err = readScheduleChange(ctx, store, token, scope, account, r)
		default:
			query, parseErr := accountPageQuery(r.URL.RawQuery)
			var after int64
			if parseErr == nil && query.After != "" {
				after, parseErr = controller.ParseScheduleID(query.After)
			}
			err = parseErr
			if err == nil {
				result, err = store.ListSchedules(ctx, token, scope, account, after, query.Limit)
			}
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if item, ok := result.(controller.Schedule); ok {
			w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
		}
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusCreated, result, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}
		if err != nil {
			logger.Warn("inventory schedule response write failed", plumelog.Fields{})
		}
	})
}

func readScheduleChange(ctx context.Context, store controller.Store, token string, scope identity.Scope, account string, r *http.Request) (controller.Schedule, error) {
	id, err := controller.ParseScheduleID(router.Param(r, "schedule"))
	if err != nil {
		return controller.Schedule{}, err
	}
	revision, ok := accountRevision(r)
	if !ok {
		return controller.Schedule{}, identity.ErrAccountStale
	}
	var input controller.ChangeSchedule
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err != nil {
		return controller.Schedule{}, err
	}
	return store.ChangeSchedule(ctx, token, scope, account, id, revision, input)
}

func inventoryScheduleOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	list := operation("listInventorySchedules", "200", "当前云账号的全量盘点计划与最近执行结果", false)
	list.Params = []openapi.Param{
		{Name: "after", In: "query", Schema: openapi.String, Description: "上一页返回的 next"},
		{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100"},
	}
	list.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	create := operation("registerInventorySchedule", "201", "为指定资源作用域登记盘点计划", true)
	update := operation("changeInventorySchedule", "200", "更新盘点间隔、启用状态或立即触发执行", true)
	for _, op := range []*openapi.Op{&create, &update} {
		op.Responses["409"] = openapi.Response{Description: "计划已存在，或账号尚不可读"}
	}
	create.Body = scheduleBody("region 和 kind 指定作用域。", map[string]openapi.Schema{
		"region": openapi.String, "kind": openapi.String, "interval_seconds": openapi.Integer, "enabled": {Type: "boolean"},
	})
	update.Body = scheduleBody("启用的计划可用 run_now 重新排队。", map[string]openapi.Schema{
		"interval_seconds": openapi.Integer, "enabled": {Type: "boolean"}, "run_now": {Type: "boolean"},
	})
	update.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	update.Responses["412"] = openapi.Response{Description: "需要当前计划版本"}
	hints["GET "+accountSchedulesRoute] = list
	hints["POST "+accountSchedulesRoute] = create
	hints["PATCH "+accountScheduleRoute] = update
}

func scheduleBody(description string, fields map[string]openapi.Schema) *openapi.RequestBody {
	return &openapi.RequestBody{Required: true, Description: description,
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: fields}}}}
}

func accountInventoryHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger, grants bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		query, err := accountPageQuery(r.URL.RawQuery)
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		var page any
		if grants {
			page, err = admin.ListGrants(ctx, token, scope, router.Param(r, "account"), query)
		} else {
			page, err = admin.List(ctx, token, scope, query)
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, page, nil); err != nil {
			logger.Warn("account inventory response write failed", plumelog.Fields{})
		}
	})
}

func accountPageQuery(raw string) (identity.AccountPageQuery, error) {
	query := identity.AccountPageQuery{Limit: identity.DefaultAccountPageLimit}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return query, identity.ErrAccountInvalid
	}
	for key, values := range params {
		if len(values) != 1 || values[0] == "" {
			return query, identity.ErrAccountInvalid
		}
		switch key {
		case "after":
			query.After = values[0]
		case "limit":
			query.Limit, err = strconv.Atoi(values[0])
			if err != nil {
				return query, identity.ErrAccountInvalid
			}
		default:
			return query, identity.ErrAccountInvalid
		}
	}
	return query, nil
}

func accountInventoryOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	for _, list := range []struct {
		route, id, description string
		fields                 map[string]openapi.Schema
	}{
		{accountsRoute, "listCloudAccounts", "当前项目自有的云账号元数据", map[string]openapi.Schema{
			"id": openapi.String, "project": openapi.String, "provider": openapi.String, "mode": openapi.String, "revision": openapi.Integer,
		}},
		{accountGrantsRoute, "listCloudAccountGrants", "当前自有账号的项目共享授权", map[string]openapi.Schema{
			"project": openapi.String, "allow_write": {Type: "boolean"},
		}},
	} {
		op := operation(list.id, "200", list.description, false)
		op.Params = []openapi.Param{
			{Name: "after", In: "query", Schema: openapi.String, Description: "同一列表上一页的 next。"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		}
		item := openapi.Schema{Type: "object", Properties: list.fields}
		response := op.Responses["200"]
		response.Content = map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object", Properties: map[string]openapi.Schema{"data": {Type: "object", Properties: map[string]openapi.Schema{
				"items": {Type: "array", Items: &item}, "next": openapi.String,
			}}},
		}}}
		op.Responses["200"] = response
		op.Responses["400"] = openapi.Response{Description: "分页参数无效"}
		hints["GET "+list.route] = op
	}
}

func accountHandler(admin identity.AccountAdmin, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		id := router.Param(r, "account")
		var account identity.Account
		var err error
		status := http.StatusOK
		switch r.Method {
		case http.MethodPost:
			var input identity.AccountInput
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
				account, err = admin.Register(ctx, token, scope, input)
			}
			status = http.StatusCreated
		case http.MethodPatch:
			revision, ok := accountRevision(r)
			if !ok {
				writeAccountError(w, r, identity.ErrAccountStale, logger)
				return
			}
			var input identity.AccountChange
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: accountInputLimit}); err == nil {
				account, err = admin.Change(ctx, token, scope, id, revision, input)
			}
		case http.MethodGet, http.MethodHead:
			account, err = admin.Get(ctx, token, scope, id)
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(account.Revision, 10)))
		if status == http.StatusCreated {
			w.Header().Set("Location", r.URL.EscapedPath()+"/"+account.ID)
			err = contract.WriteResponse(w, r, http.StatusCreated, account, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, account, nil)
		}
		if err != nil {
			logger.Warn("account response write failed", plumelog.Fields{})
		}
	})
}

func accountRevision(r *http.Request) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 || !strings.HasPrefix(values[0], `"`) || !strings.HasSuffix(values[0], `"`) || len(values[0]) < 3 {
		return 0, false
	}
	revision, err := strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	return revision, err == nil && revision > 0 && strconv.Quote(strconv.FormatInt(revision, 10)) == values[0]
}

func writeAccountError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	if apiErr, ok := contract.AsAPIError(err); ok {
		if err := contract.WriteError(w, r, apiErr); err != nil {
			logger.Warn("account error response write failed", plumelog.Fields{})
		}
		return
	}
	kind, message := contract.TypeUnavailable, "account administration is unavailable"
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-manager"`)
		kind, message = contract.TypeUnauthorized, "authentication required"
	case errors.Is(err, identity.ErrForbidden):
		kind, message = contract.TypeForbidden, "account or project access is unavailable"
	case errors.Is(err, identity.ErrAccountInvalid):
		kind, message = contract.TypeBadRequest, "invalid account action or parameters"
	case errors.Is(err, identity.ErrAccountStale):
		kind, message = contract.TypePreconditionFailed, "current account ETag required"
	case errors.Is(err, identity.ErrAccountConflict):
		kind, message = contract.TypeConflict, "account action conflicts with current state"
	}
	if err := contract.WriteError(w, r, contract.NewErrorBuilder().Type(kind).Message(message).Build()); err != nil {
		logger.Warn("account error response write failed", plumelog.Fields{})
	}
}
