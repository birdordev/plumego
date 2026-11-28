package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	operationhttp "with-cloud-manager/app/handlers/operations"
	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const CatalogRoute = "/v1/tenants/:tenant/projects/:project/service-catalog"
const CatalogItemRoute = CatalogRoute + "/:template"

func RegisterCatalog(app *core.App, store operations.Store) error {
	handler := catalogHTTP{store: store, logger: app.Logger()}
	return errors.Join(
		app.Get(CatalogRoute, handler.endpoint(handler.list)),
		app.Post(CatalogRoute, handler.endpoint(handler.change)),
		app.Get(CatalogItemRoute, handler.endpoint(handler.get)),
		app.Put(CatalogItemRoute, handler.endpoint(handler.change)),
		app.Delete(CatalogItemRoute, handler.endpoint(handler.change)),
		app.Get(CatalogItemRoute+"/versions/:version", handler.endpoint(handler.version)),
		app.Post(CatalogItemRoute+"/launch", handler.endpoint(handler.launch)),
	)
}

type catalogHTTP struct {
	store  operations.Store
	logger plumelog.StructuredLogger
}

func (h catalogHTTP) endpoint(f func(context.Context, http.ResponseWriter, *http.Request, string, identity.Scope)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, h.logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		f(ctx, w, r, token, scope)
	})
}

type catalogQuery struct {
	includeDisabled bool
	after           string
	limit           int
}

func readCatalogQuery(raw string) (catalogQuery, error) {
	query := catalogQuery{limit: 50}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return query, operations.ErrInvalid
	}
	for name, list := range values {
		if len(list) != 1 {
			return query, operations.ErrInvalid
		}
		switch name {
		case "include_disabled":
			if list[0] != "true" && list[0] != "false" {
				return query, operations.ErrInvalid
			}
			query.includeDisabled = list[0] == "true"
		case "after":
			query.after = list[0]
		case "limit":
			query.limit, err = strconv.Atoi(list[0])
			if err != nil || query.limit < 1 || query.limit > 100 {
				return query, operations.ErrInvalid
			}
		default:
			return query, operations.ErrInvalid
		}
	}
	return query, nil
}

func (h catalogHTTP) list(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	query, err := readCatalogQuery(r.URL.RawQuery)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	page, err := h.store.ListCatalog(ctx, token, scope, query.includeDisabled, query.after, query.limit)
	h.writeResult(w, r, page, err)
}

func (h catalogHTTP) get(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	item, err := h.store.GetCatalog(ctx, token, scope, router.Param(r, "template"))
	if err == nil {
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
	}
	h.writeResult(w, r, item, err)
}

func (h catalogHTTP) version(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	value := router.Param(r, "version")
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(version, 10) != value {
		h.writeError(w, r, operations.ErrInvalid)
		return
	}
	item, err := h.store.GetCatalogVersion(ctx, token, scope, router.Param(r, "template"), version)
	h.writeResult(w, r, item, err)
}

func (h catalogHTTP) change(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	var item operations.CatalogItem
	var err error
	if r.Method == http.MethodPost {
		var input operations.CatalogCreate
		if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err == nil {
			item, err = h.store.CreateCatalog(ctx, token, scope, input)
		}
	} else {
		revision, ok := transport.AccountRevision(r)
		if !ok {
			h.writeError(w, r, operations.ErrCatalogStale)
			return
		}
		id := router.Param(r, "template")
		if r.Method == http.MethodDelete {
			item, err = h.store.RetireCatalog(ctx, token, scope, id, revision)
		} else {
			var input operations.CatalogSpec
			if err = contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err == nil {
				item, err = h.store.PublishCatalog(ctx, token, scope, id, revision, input)
			}
		}
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
	if r.Method == http.MethodPost {
		w.Header().Set("Location", r.URL.EscapedPath()+"/"+item.ID)
		if err := contract.WriteResponse(w, r, http.StatusCreated, item, nil); err != nil {
			h.logger.Warn("catalog response write failed", plumelog.Fields{})
		}
		return
	}
	h.writeResult(w, r, item, nil)
}

func (h catalogHTTP) launch(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		h.writeError(w, r, operations.ErrInvalid)
		return
	}
	var input operations.CatalogLaunch
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
		h.writeError(w, r, err)
		return
	}
	op, err := h.store.SubmitCatalogCreation(ctx, token, scope, keys[0], router.Param(r, "template"), input)
	if errors.Is(err, operations.ErrCatalogStale) {
		h.writeError(w, r, err)
		return
	}
	operationhttp.WriteOperationReceipt(w, r, scope, op, err, h.logger)
}

func (h catalogHTTP) writeResult(w http.ResponseWriter, r *http.Request, result any, err error) {
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := contract.WriteResponse(w, r, http.StatusOK, result, nil); err != nil {
		h.logger.Warn("catalog response write failed", plumelog.Fields{})
	}
}

func (h catalogHTTP) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, operations.ErrInvalid):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid service catalog parameters or dependencies").Build()
	case errors.Is(err, operations.ErrConflict):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("catalog template ID already exists").Build()
	case errors.Is(err, operations.ErrCatalogStale):
		err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current catalog revision or enabled version required").Build()
	}
	transport.WriteAccountError(w, r, err, h.logger)
}
