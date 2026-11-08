// Package rest provides REST resource controller primitives, query helpers, and
// pagination utilities for building CRUD HTTP APIs on top of the standard library.
//
// These types were previously part of the router package. The router package now
// focuses on routing concerns only (path matching, parameter extraction, middleware
// composition). Application-level REST scaffolding lives here.
//
// Stability: stable extension interface layer — not part of the minimal core runtime.
//
// x/rest is the shared home for reusable, transport-facing resource patterns:
// query parsing, pagination, hookable CRUD controllers, and repository-backed
// resource wiring. Use it when the goal is to standardize resource APIs across
// services rather than to define the application's bootstrap shape.
package rest

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
)

// ================================================
// ResourceController — RESTful resource interface
// ================================================

// ResourceController defines the interface for RESTful resource controllers.
// Implement this interface and register it with Router.Resource to get full REST routes.
type ResourceController interface {
	Index(http.ResponseWriter, *http.Request)       // GET /resource
	Show(http.ResponseWriter, *http.Request)        // GET /resource/:id
	Create(http.ResponseWriter, *http.Request)      // POST /resource
	Update(http.ResponseWriter, *http.Request)      // PUT /resource/:id
	Delete(http.ResponseWriter, *http.Request)      // DELETE /resource/:id
	Patch(http.ResponseWriter, *http.Request)       // PATCH /resource/:id
	Options(http.ResponseWriter, *http.Request)     // OPTIONS /resource
	Head(http.ResponseWriter, *http.Request)        // HEAD /resource
	BatchCreate(http.ResponseWriter, *http.Request) // POST /resource/batch
	BatchDelete(http.ResponseWriter, *http.Request) // DELETE /resource/batch
}

// BaseResourceController provides a default implementation of ResourceController.
// All methods return "Not Implemented" by default; override the ones you need.
type BaseResourceController struct {
	ResourceName string
}

// NewBaseResourceController creates a new base resource controller.
func NewBaseResourceController(resourceName string) *BaseResourceController {
	return &BaseResourceController{
		ResourceName: resourceName,
	}
}

func (c *BaseResourceController) Index(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Index")
}
func (c *BaseResourceController) Show(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Show")
}
func (c *BaseResourceController) Create(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Create")
}
func (c *BaseResourceController) Update(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Update")
}
func (c *BaseResourceController) Delete(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Delete")
}
func (c *BaseResourceController) Patch(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "Patch")
}

// Options handles OPTIONS requests; sets common CORS headers.
func (c *BaseResourceController) Options(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS, HEAD")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// Head handles HEAD requests; returns 200 OK with no body.
func (c *BaseResourceController) Head(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (c *BaseResourceController) BatchCreate(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "BatchCreate")
}
func (c *BaseResourceController) BatchDelete(w http.ResponseWriter, r *http.Request) {
	writeNotImplementedResourceError(w, r, c.resourceName(), "BatchDelete")
}

func (c *BaseResourceController) resourceName() string {
	if c == nil || strings.TrimSpace(c.ResourceName) == "" {
		return "resource"
	}
	return c.ResourceName
}

func writeNotImplementedResourceError(w http.ResponseWriter, r *http.Request, resourceName, method string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeNotImplemented).
		Message(fmt.Sprintf("the %s method is not implemented for the %s resource", method, resourceName)).
		Detail("method", method).
		Detail("resource", resourceName).
		Build())
}

// ================================================
// QueryParams — HTTP query parameter parsing
// ================================================

// QueryParams represents parsed query parameters for list operations.
type QueryParams struct {
	Page     int
	PageSize int
	Offset   int
	Limit    int

	Sort []SortField

	Filters map[string]string
	Search  string
	Fields  []string
	Include []string
}

// SortField represents a sort specification.
type SortField struct {
	Field string
	Desc  bool
}

// QueryBuilder builds QueryParams from an *http.Request.
type QueryBuilder struct {
	defaultPageSize int
	maxPageSize     int
	allowedSorts    map[string]bool
	allowedFilters  map[string]bool
}

// NewQueryBuilder creates a QueryBuilder with sensible defaults (page 20, max 100).
func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{
		defaultPageSize: 20,
		maxPageSize:     100,
		allowedSorts:    make(map[string]bool),
		allowedFilters:  make(map[string]bool),
	}
}

// NewQueryBuilderFromOptions creates a QueryBuilder driven by resource options.
func NewQueryBuilderFromOptions(opts *ResourceOptions) *QueryBuilder {
	qb := NewQueryBuilder()
	if opts == nil {
		return qb
	}

	defaultSize := opts.DefaultPageSize
	if defaultSize <= 0 {
		defaultSize = qb.defaultPageSize
	}

	maxSize := opts.MaxPageSize
	if maxSize <= 0 {
		maxSize = qb.maxPageSize
	}
	if maxSize < defaultSize {
		maxSize = defaultSize
	}

	qb.WithPageSize(defaultSize, maxSize)
	if len(opts.AllowedSorts) > 0 {
		qb.WithAllowedSorts(opts.AllowedSorts...)
	}
	if len(opts.AllowedFilters) > 0 {
		qb.WithAllowedFilters(opts.AllowedFilters...)
	}

	return qb
}

// WithPageSize sets the default and maximum page size.
func (qb *QueryBuilder) WithPageSize(defaultSize, maxSize int) *QueryBuilder {
	qb.defaultPageSize = defaultSize
	qb.maxPageSize = maxSize
	return qb
}

// WithAllowedSorts sets the allowed sort fields; unknown fields are silently ignored.
func (qb *QueryBuilder) WithAllowedSorts(fields ...string) *QueryBuilder {
	qb.allowedSorts = make(map[string]bool, len(fields))
	for _, f := range fields {
		qb.allowedSorts[f] = true
	}
	return qb
}

// WithAllowedFilters sets the allowed filter fields; unknown fields are silently ignored.
func (qb *QueryBuilder) WithAllowedFilters(fields ...string) *QueryBuilder {
	qb.allowedFilters = make(map[string]bool, len(fields))
	for _, f := range fields {
		qb.allowedFilters[f] = true
	}
	return qb
}

// Parse extracts QueryParams from an HTTP request.
func (qb *QueryBuilder) Parse(r *http.Request) *QueryParams {
	query := r.URL.Query()
	params := &QueryParams{
		Page:     1,
		PageSize: qb.defaultPageSize,
		Filters:  make(map[string]string),
	}

	if pageStr := query.Get("page"); pageStr != "" {
		if page, err := strconv.Atoi(pageStr); err == nil && page > 0 {
			params.Page = page
		}
	}
	if pageSizeStr := query.Get("page_size"); pageSizeStr != "" {
		if pageSize, err := strconv.Atoi(pageSizeStr); err == nil && pageSize > 0 {
			params.PageSize = pageSize
			if params.PageSize > qb.maxPageSize {
				params.PageSize = qb.maxPageSize
			}
		}
	}
	if limitStr := query.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			params.Limit = limit
			if params.Limit > qb.maxPageSize {
				params.Limit = qb.maxPageSize
			}
		}
	}
	if offsetStr := query.Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil && offset >= 0 {
			params.Offset = offset
		}
	}
	if params.Offset == 0 && params.Page > 1 {
		params.Offset = (params.Page - 1) * params.PageSize
	}
	if params.Limit == 0 {
		params.Limit = params.PageSize
	}

	// Multi-field sort: sort=field1,-field2 (- prefix = descending)
	if sortStr := query.Get("sort"); sortStr != "" {
		for _, field := range strings.Split(sortStr, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			desc := strings.HasPrefix(field, "-")
			if desc {
				field = field[1:]
			}
			if field == "" {
				continue
			}
			if len(qb.allowedSorts) > 0 && !qb.allowedSorts[field] {
				continue
			}
			params.Sort = append(params.Sort, SortField{Field: field, Desc: desc})
		}
	}

	params.Search = query.Get("search")
	if params.Search == "" {
		params.Search = query.Get("q")
	}

	skip := map[string]bool{
		"page": true, "page_size": true, "limit": true, "offset": true,
		"sort":   true,
		"search": true, "q": true, "fields": true, "include": true,
	}
	for key, values := range query {
		if len(values) == 0 || skip[key] {
			continue
		}
		if len(qb.allowedFilters) > 0 && !qb.allowedFilters[key] {
			continue
		}
		params.Filters[key] = values[0]
	}

	if fieldsStr := query.Get("fields"); fieldsStr != "" {
		for _, f := range strings.Split(fieldsStr, ",") {
			if f = strings.TrimSpace(f); f != "" {
				params.Fields = append(params.Fields, f)
			}
		}
	}
	if includeStr := query.Get("include"); includeStr != "" {
		for _, inc := range strings.Split(includeStr, ",") {
			if inc = strings.TrimSpace(inc); inc != "" {
				params.Include = append(params.Include, inc)
			}
		}
	}

	return params
}

// ================================================
// Pagination
// ================================================

// PaginationMeta holds pagination metadata for list responses.
type PaginationMeta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

// NewPaginationMeta builds PaginationMeta from page, page size, and total item count.
// A pageSize ≤ 0 is treated as 1 to avoid division-by-zero.
func NewPaginationMeta(page, pageSize int, totalItems int64) *PaginationMeta {
	if pageSize <= 0 {
		pageSize = 1
	}
	totalPages := int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 0 {
		totalPages = 0
	}
	return &PaginationMeta{
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
		HasPrev:    page > 1,
	}
}

// PaginatedResponse is the canonical envelope for paginated list responses.
type PaginatedResponse struct {
	Data       any             `json:"data"`
	Pagination *PaginationMeta `json:"pagination,omitempty"`
}

// ================================================
// Resource lifecycle hooks
// ================================================

// ResourceHooks defines lifecycle callbacks for resource CRUD operations.
type ResourceHooks interface {
	BeforeCreate(ctx context.Context, data any) error
	AfterCreate(ctx context.Context, data any) error
	BeforeUpdate(ctx context.Context, id string, data any) error
	AfterUpdate(ctx context.Context, id string, data any) error
	BeforeDelete(ctx context.Context, id string) error
	AfterDelete(ctx context.Context, id string) error
	BeforeList(ctx context.Context, params *QueryParams) error
	AfterList(ctx context.Context, params *QueryParams, data any) error
}

// NoOpResourceHooks is a no-op implementation of ResourceHooks.
// Embed it in your hook struct and override only the methods you need.
type NoOpResourceHooks struct{}

func (h *NoOpResourceHooks) BeforeCreate(_ context.Context, _ any) error              { return nil }
func (h *NoOpResourceHooks) AfterCreate(_ context.Context, _ any) error               { return nil }
func (h *NoOpResourceHooks) BeforeUpdate(_ context.Context, _ string, _ any) error    { return nil }
func (h *NoOpResourceHooks) AfterUpdate(_ context.Context, _ string, _ any) error     { return nil }
func (h *NoOpResourceHooks) BeforeDelete(_ context.Context, _ string) error           { return nil }
func (h *NoOpResourceHooks) AfterDelete(_ context.Context, _ string) error            { return nil }
func (h *NoOpResourceHooks) BeforeList(_ context.Context, _ *QueryParams) error       { return nil }
func (h *NoOpResourceHooks) AfterList(_ context.Context, _ *QueryParams, _ any) error { return nil }

// ================================================
// Resource transformer
// ================================================

// ResourceTransformer transforms resource entities before they are serialized.
type ResourceTransformer interface {
	Transform(ctx context.Context, resource any) (any, error)
	TransformCollection(ctx context.Context, resources any) (any, error)
}

// IdentityTransformer returns resources unchanged (pass-through).
type IdentityTransformer struct{}

func (t *IdentityTransformer) Transform(_ context.Context, resource any) (any, error) {
	return resource, nil
}
func (t *IdentityTransformer) TransformCollection(_ context.Context, resources any) (any, error) {
	return resources, nil
}

// ================================================
// ParamExtractor — request parameter helpers
// ================================================

// ParamExtractor provides utilities for extracting path and query parameters from requests.
type ParamExtractor struct{}

// NewParamExtractor creates a new ParamExtractor.
func NewParamExtractor() *ParamExtractor { return &ParamExtractor{} }

// GetID returns the ":id" path parameter from the request context.
func (pe *ParamExtractor) GetID(r *http.Request) string {
	return contract.RequestContextFromContext(r.Context()).Params["id"]
}

// GetParam returns a named path parameter from the request context.
func (pe *ParamExtractor) GetParam(r *http.Request, name string) string {
	return contract.RequestContextFromContext(r.Context()).Params[name]
}

// GetQueryParam returns a query parameter value.
func (pe *ParamExtractor) GetQueryParam(r *http.Request, name string) string {
	return r.URL.Query().Get(name)
}

// GetQueryInt parses an integer query parameter, returning defaultValue on parse failure.
func (pe *ParamExtractor) GetQueryInt(r *http.Request, name string, defaultValue int) int {
	if str := r.URL.Query().Get(name); str != "" {
		if val, err := strconv.Atoi(str); err == nil {
			return val
		}
	}
	return defaultValue
}

// GetQueryBool parses a boolean query parameter, returning defaultValue on parse failure.
func (pe *ParamExtractor) GetQueryBool(r *http.Request, name string, defaultValue bool) bool {
	if str := r.URL.Query().Get(name); str != "" {
		if val, err := strconv.ParseBool(str); err == nil {
			return val
		}
	}
	return defaultValue
}

// ================================================
// Context-aware resource controller
// ================================================

// BaseContextResourceController provides a default implementation with context support.
// Embed it in your controller struct and override the methods you need.
type BaseContextResourceController struct {
	BaseResourceController
	QueryBuilder   *QueryBuilder
	ParamExtractor *ParamExtractor
	Hooks          ResourceHooks
	Transformer    ResourceTransformer
	Spec           ResourceSpec
}

// NewBaseContextResourceController creates a context-aware resource controller with defaults.
func NewBaseContextResourceController(resourceName string) *BaseContextResourceController {
	controller := &BaseContextResourceController{
		BaseResourceController: BaseResourceController{ResourceName: resourceName},
	}
	ApplyResourceSpec(controller, DefaultResourceSpec(resourceName))
	return controller
}

// ApplySpec applies the reusable resource specification to the controller.
func (c *BaseContextResourceController) ApplySpec(spec ResourceSpec) *BaseContextResourceController {
	ApplyResourceSpec(c, spec)
	return c
}

// ParseQueryParams parses and normalizes resource query params from the request
// using the controller's spec-driven defaults.
func (c *BaseContextResourceController) ParseQueryParams(r *http.Request) *QueryParams {
	if c == nil {
		return NormalizeQueryParams(nil, nil)
	}

	builder := c.QueryBuilder
	if builder == nil {
		builder = queryBuilderFromSpec(c.Spec)
		c.QueryBuilder = builder
	}

	params := builder.Parse(r)
	return NormalizeQueryParams(params, c.Spec.Options)
}

// ================================================
// ResourceOptions
// ================================================

// ResourceOptions configures resource controller behavior.
type ResourceOptions struct {
	DefaultPageSize   int
	MaxPageSize       int
	AllowedSorts      []string
	AllowedFilters    []string
	SoftDelete        bool
	SoftDeleteField   string
	EnableHooks       bool
	EnableTransformer bool
}

// DefaultResourceOptions returns sensible defaults.
func DefaultResourceOptions() *ResourceOptions {
	return &ResourceOptions{
		DefaultPageSize:   20,
		MaxPageSize:       100,
		AllowedSorts:      []string{},
		AllowedFilters:    []string{},
		SoftDelete:        false,
		SoftDeleteField:   "deleted_at",
		EnableHooks:       true,
		EnableTransformer: false,
	}
}

// NormalizeQueryParams applies resource-level defaults and allowlists to parsed query params.
func NormalizeQueryParams(params *QueryParams, opts *ResourceOptions) *QueryParams {
	if params == nil {
		params = &QueryParams{}
	}
	if opts == nil {
		return params
	}

	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 && opts.DefaultPageSize > 0 {
		params.PageSize = opts.DefaultPageSize
	}
	if opts.MaxPageSize > 0 && params.PageSize > opts.MaxPageSize {
		params.PageSize = opts.MaxPageSize
	}
	if params.PageSize > 0 {
		params.Limit = params.PageSize
		params.Offset = (params.Page - 1) * params.PageSize
	}

	if len(opts.AllowedFilters) > 0 && len(params.Filters) > 0 {
		allowed := make(map[string]struct{}, len(opts.AllowedFilters))
		for _, name := range opts.AllowedFilters {
			allowed[name] = struct{}{}
		}

		filtered := make(map[string]string, len(params.Filters))
		for key, value := range params.Filters {
			if _, ok := allowed[key]; ok {
				filtered[key] = value
			}
		}
		params.Filters = filtered
	}

	if len(opts.AllowedSorts) > 0 && len(params.Sort) > 0 {
		allowed := make(map[string]struct{}, len(opts.AllowedSorts))
		for _, name := range opts.AllowedSorts {
			allowed[name] = struct{}{}
		}

		filtered := make([]SortField, 0, len(params.Sort))
		for _, sort := range params.Sort {
			if _, ok := allowed[sort.Field]; ok {
				filtered = append(filtered, sort)
			}
		}
		params.Sort = filtered
	}

	return params
}

// ================================================
// Batch processing
// ================================================

// BatchResult is the result of a batch operation.
type BatchResult struct {
	Successful int              `json:"successful"`
	Failed     int              `json:"failed"`
	Total      int              `json:"total"`
	Errors     []BatchError     `json:"errors,omitempty"`
	Results    []map[string]any `json:"results,omitempty"`
}

// BatchError describes an individual failure within a batch operation.
type BatchError struct {
	Index   int    `json:"index"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// BatchProcessor handles batch operations with a configurable item limit.
type BatchProcessor struct {
	maxBatchSize int
}

// NewBatchProcessor creates a BatchProcessor. maxBatchSize ≤ 0 defaults to 100.
func NewBatchProcessor(maxBatchSize int) *BatchProcessor {
	if maxBatchSize <= 0 {
		maxBatchSize = 100
	}
	return &BatchProcessor{maxBatchSize: maxBatchSize}
}

// Process runs fn for each item. Items beyond maxBatchSize produce a single BATCH_TOO_LARGE error.
func (bp *BatchProcessor) Process(
	ctx context.Context,
	items []any,
	fn func(ctx context.Context, item any) error,
) *BatchResult {
	result := &BatchResult{
		Total:   len(items),
		Errors:  []BatchError{},
		Results: []map[string]any{},
	}

	if len(items) > bp.maxBatchSize {
		result.Failed = len(items)
		result.Errors = append(result.Errors, BatchError{
			Message: fmt.Sprintf("Batch size exceeds maximum of %d", bp.maxBatchSize),
			Code:    "BATCH_TOO_LARGE",
		})
		return result
	}

	for i, item := range items {
		if err := fn(ctx, item); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, BatchError{
				Index:   i,
				Message: err.Error(),
				Code:    "PROCESSING_ERROR",
			})
		} else {
			result.Successful++
		}
	}

	return result
}
