package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"standard-service/internal/domain/item"
)

const (
	codeItemCreateInvalidJSON    = "item.create.invalid_json"
	codeItemCreateBodyRequired   = "item.create.body_required"
	codeItemCreateFailed         = "item.create.failed"
	codeItemFieldsRequired       = "item.fields.required"
	codeItemUpdateFieldsRequired = "item.update.fields_required"
	codeItemUpdateFailed         = "item.update.failed"
	codeItemNotFound             = "item.not_found"
	codeItemUpdateInvalidJSON    = "item.update.invalid_json"
	codeItemUpdateBodyRequired   = "item.update.body_required"
	codeItemCreateUnknownField   = "item.create.unknown_field"
	codeItemUpdateUnknownField   = "item.update.unknown_field"
	codeItemListFailed           = "item.list.failed"
	codeItemListInvalidParam     = "item.list.invalid_param"
	codeItemPatchInvalidJSON     = "item.patch.invalid_json"
	codeItemPatchBodyRequired    = "item.patch.body_required"
	codeItemPatchUnknownField    = "item.patch.unknown_field"
	codeItemPatchNoFields        = "item.patch.no_fields"
	codeItemPatchFailed          = "item.patch.failed"
	codeItemDeleteFailed         = "item.delete.failed"
)

// noLimit is passed as maxVal to parseQueryInt when no upper bound is needed.
// Using math.MaxInt means the upper-bound rejection (n > maxVal) will never
// trigger for any realistic query parameter value.
const noLimit = math.MaxInt

// ItemService is the minimal business contract that ItemHandler depends on.
// Declaring this interface in the handler package means the handler owns its
// dependency contract and is not coupled to domain implementation details.
//
// In production routes.go wires item.ItemService, which implements this interface
// via structural typing. In handler tests item.MemoryStore also satisfies it
// directly, allowing test setup to inject storage straight into the handler
// without constructing the service layer.
//
// The error return on mutating methods distinguishes storage failures (non-nil error)
// from "not found" (bool false, nil error) so handlers can return 500 vs 404.
type ItemService interface {
	Create(ctx context.Context, name, description string) (item.Item, error)
	Get(ctx context.Context, id string) (item.Item, bool)
	List(ctx context.Context, offset, limit int) ([]item.Item, int, error)
	Update(ctx context.Context, id, name, description string) (item.Item, bool, error)
	Patch(ctx context.Context, id, name, description string) (item.Item, bool, error)
	Delete(ctx context.Context, id string) (bool, error)
}

// ItemHandler demonstrates constructor injection: declare the dependency as an
// interface field and wire the concrete implementation in routes.go.
//
// Logger must not be nil; pass a.Core.Logger() from routes.go.
// Use plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatDiscard}) in tests.
type ItemHandler struct {
	Service ItemService
	Logger  plumelog.StructuredLogger
}

// itemWriteReq is the shared request body for create, update, and patch operations.
// All three operations use the same fields; the per-operation validation semantics
// (Create/Update require both non-empty; Patch requires at least one) are enforced
// at the handler level, not the type level.
type itemWriteReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// requireItemFields checks that name and description are both non-empty.
// It adds a per-field detail entry to eb for each empty field and returns false
// when at least one is missing, so the caller can return a single error response
// that names every problem the client needs to fix in one round trip.
func requireItemFields(eb *contract.ErrorBuilder, name, description string) (*contract.ErrorBuilder, bool) {
	valid := true
	if name == "" {
		eb = eb.Detail("name", "field is required")
		valid = false
	}
	if description == "" {
		eb = eb.Detail("description", "field is required")
		valid = false
	}
	return eb, valid
}

// decodeJSONStrict decodes exactly one JSON object from r.Body into dst and
// rejects any field that does not map to a struct field (DisallowUnknownFields).
// It returns the decoder error unchanged so callers can distinguish io.EOF
// (empty body), an unknown-field error (see unknownFieldName), and malformed
// JSON, mapping each to the appropriate transport response.
//
// This is the canonical decode path for write handlers that own a fixed request
// shape. Use the lenient json.NewDecoder(r.Body).Decode(...) form only when the
// endpoint must tolerate forward-compatible extra fields.
func decodeJSONStrict(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// unknownFieldName returns the offending field name when err is the error
// encoding/json reports for an unknown field under DisallowUnknownFields
// (`json: unknown field "x"`), or "" for any other error. The standard library
// does not expose a typed error for this case, so the documented message prefix
// is matched directly.
//
// TestUnknownFieldNameAnchorsStdlibFormat pins the exact message format used here
// so a Go version upgrade that changes the message text will surface as a test
// failure rather than a silent fallback to the generic malformed-JSON error response.
func unknownFieldName(err error) string {
	const prefix = `json: unknown field "`
	if err == nil {
		return ""
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	rest := msg[len(prefix):]
	if i := strings.IndexByte(rest, '"'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// Create handles POST /api/v1/items.
// It demonstrates the canonical write-decode error patterns:
//   - decode errors are caught first and returned immediately, with empty body,
//     unknown field, and malformed JSON each mapped to a distinct error code
//   - field validation then collects ALL missing required fields before writing an
//     error response, so clients receive a single actionable reply that names
//     every field they need to fix rather than discovering failures one at a time
//
// Decoding is strict (decodeJSONStrict): a client typo such as {"naem":"x"} is
// rejected with a clear unknown-field error instead of being silently dropped and
// later surfacing as a confusing "field required" response.
//
// On a 5xx storage error, the request_id is included in the response detail so
// callers can quote it to the support team; the full error is logged at Error
// level server-side so operators can trace it without exposing internal detail.
//
// Note on decode-error structure: the three-branch decode block (EOF / unknown
// field / malformed JSON) is intentionally inlined in each write handler rather
// than extracted into a shared helper. The repo conformance test requires that
// every Code(...) argument is a traceable string constant; passing codes through
// a struct or function parameter obscures that traceability.
//
// Examples:
//
//	POST /api/v1/items {"name":"widget","description":"a widget"}  → 201 item
//	POST /api/v1/items                                             → 400 TypeRequired  (empty body)
//	POST /api/v1/items (bad JSON)                                  → 400 TypeBadRequest
//	POST /api/v1/items {"name":"x","desc":"y"}                     → 400 TypeBadRequest unknown field "desc"
//	POST /api/v1/items {}                                          → 400 TypeRequired  details: name + description
//	POST /api/v1/items {"name":"widget"}                           → 400 TypeRequired  detail: description
func (h ItemHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req itemWriteReq
	if err := decodeJSONStrict(r, &req); err != nil {
		if errors.Is(err, io.EOF) {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRequired).
				Code(codeItemCreateBodyRequired).
				Message("request body is required").
				Build()))
			return
		}
		if field := unknownFieldName(err); field != "" {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeBadRequest).
				Code(codeItemCreateUnknownField).
				Detail("field", field).
				Message("request body contains an unknown field").
				Build()))
			return
		}
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(codeItemCreateInvalidJSON).
			Message("request body must be valid JSON").
			Build()))
		return
	}

	eb := contract.NewErrorBuilder().
		Type(contract.TypeRequired).
		Code(codeItemFieldsRequired).
		Message("one or more required fields are missing")
	if eb, ok := requireItemFields(eb, req.Name, req.Description); !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, eb.Build()))
		return
	}

	created, err := h.Service.Create(r.Context(), req.Name, req.Description)
	if err != nil {
		requestID := contract.RequestIDFromContext(r.Context())
		h.Logger.Error("item create failed", plumelog.Fields{
			"request_id": requestID,
			"err":        err.Error(),
		})
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(codeItemCreateFailed).
			Detail("request_id", requestID).
			Message("failed to create item").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusCreated, created, nil))
}

// List handles GET /api/v1/items with basic limit/offset pagination.
// Items are returned in stable creation order.
// Pagination metadata (total, limit, offset) is carried in the response meta field,
// not embedded in the data payload, demonstrating the canonical meta parameter usage.
// The meta offset field reflects the requested offset, not a clamped internal value,
// so clients always know exactly where their page begins relative to the full list.
// Pagination is delegated to the repository so real storage backends can issue a
// single LIMIT/OFFSET query rather than loading every row into memory.
//
//	GET /api/v1/items                    → 200 data:[…] meta:{total:N,limit:20,offset:0}
//	GET /api/v1/items?limit=5            → first 5 items (limit must be 1–100)
//	GET /api/v1/items?limit=5&offset=10  → items 11-15
//	GET /api/v1/items?offset=999         → empty data; meta:{total:N,limit:20,offset:999}
//	GET /api/v1/items?limit=abc          → 400 TypeBadRequest item.list.invalid_param
//	GET /api/v1/items?limit=200          → 400 TypeBadRequest item.list.invalid_param (exceeds max 100)
func (h ItemHandler) List(w http.ResponseWriter, r *http.Request) {
	const defaultLimit = 20
	const maxLimit = 100

	limit, ok := parseQueryInt(r, "limit", defaultLimit, 1, maxLimit)
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(codeItemListInvalidParam).
			Detail("field", "limit").
			Message(fmt.Sprintf("limit must be an integer between 1 and %d", maxLimit)).
			Build()))
		return
	}

	offset, ok := parseQueryInt(r, "offset", 0, 0, noLimit)
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(codeItemListInvalidParam).
			Detail("field", "offset").
			Message("offset must be a non-negative integer").
			Build()))
		return
	}

	// Delegate pagination to the repository. The meta offset field carries the
	// requested offset (not a clamped value) so clients always know exactly where
	// their page was requested relative to the full list.
	page, total, err := h.Service.List(r.Context(), offset, limit)
	if err != nil {
		requestID := contract.RequestIDFromContext(r.Context())
		h.Logger.Error("item list failed", plumelog.Fields{
			"request_id": requestID,
			"err":        err.Error(),
		})
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(codeItemListFailed).
			Detail("request_id", requestID).
			Message("failed to list items").
			Build()))
		return
	}

	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, page, map[string]any{
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}))
}

// parseQueryInt parses an integer query parameter.
// Returns (defaultVal, true) when the parameter is absent.
// Returns (0, false) when the parameter is present but not a valid integer, is
// below minVal, or is above maxVal; the caller should write a 400 error in that
// case so an out-of-range value is reported explicitly rather than being clamped
// to a different page size than the client asked for.
// Pass noLimit (math.MaxInt) for maxVal to allow any value at or above minVal.
func parseQueryInt(r *http.Request, key string, defaultVal, minVal, maxVal int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultVal, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minVal || n > maxVal {
		return 0, false
	}
	return n, true
}

// GetByID handles GET /api/v1/items/:id.
// It demonstrates path parameter extraction and the canonical 404 error path.
//
//	GET /api/v1/items/<id>    → 200 item
//	GET /api/v1/items/missing → 404 TypeNotFound
func (h ItemHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := router.Param(r, "id")
	found, ok := h.Service.Get(r.Context(), id)
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(codeItemNotFound).
			Detail("id", id).
			Message("item not found").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, found, nil))
}

// Update handles PUT /api/v1/items/:id.
// PUT is idempotent and replaces all mutable fields (name, description).
// id and created_at are immutable and are never replaced.
// Collect all missing required fields before writing an error response,
// matching the same pattern as Create so clients fix everything in one round trip.
//
//	PUT /api/v1/items/<id> {"name":"renamed","description":"new"}  → 200 updated item
//	PUT /api/v1/items/<id>                                         → 400 TypeRequired (empty body)
//	PUT /api/v1/items/<id> {}                                      → 400 TypeRequired (name + description)
//	PUT /api/v1/items/missing {"name":"x","description":"y"}       → 404 TypeNotFound
//	PUT /api/v1/items/<id> {"name":"x","desc":"y"}                 → 400 TypeBadRequest unknown field "desc"
//
// Decoding is strict (decodeJSONStrict), matching Create: unknown fields are
// rejected rather than silently ignored.
func (h ItemHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := router.Param(r, "id")

	var req itemWriteReq
	if err := decodeJSONStrict(r, &req); err != nil {
		if errors.Is(err, io.EOF) {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRequired).
				Code(codeItemUpdateBodyRequired).
				Message("request body is required").
				Build()))
			return
		}
		if field := unknownFieldName(err); field != "" {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeBadRequest).
				Code(codeItemUpdateUnknownField).
				Detail("field", field).
				Message("request body contains an unknown field").
				Build()))
			return
		}
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(codeItemUpdateInvalidJSON).
			Message("request body must be valid JSON").
			Build()))
		return
	}

	eb := contract.NewErrorBuilder().
		Type(contract.TypeRequired).
		Code(codeItemUpdateFieldsRequired).
		Message("one or more required fields are missing")
	if eb, ok := requireItemFields(eb, req.Name, req.Description); !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, eb.Build()))
		return
	}

	updated, ok, err := h.Service.Update(r.Context(), id, req.Name, req.Description)
	if err != nil {
		requestID := contract.RequestIDFromContext(r.Context())
		h.Logger.Error("item update failed", plumelog.Fields{
			"request_id": requestID,
			"item_id":    id,
			"err":        err.Error(),
		})
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(codeItemUpdateFailed).
			Detail("request_id", requestID).
			Message("failed to update item").
			Build()))
		return
	}
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(codeItemNotFound).
			Detail("id", id).
			Message("item not found").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, updated, nil))
}

// Patch handles PATCH /api/v1/items/:id.
// Unlike PUT, PATCH applies a partial update: only the non-empty fields in the
// request body are written; fields absent or empty in the body are left unchanged.
// At least one of name or description must be non-empty.
//
//	PATCH /api/v1/items/<id> {"name":"renamed"}                          → 200 (name changed, description preserved)
//	PATCH /api/v1/items/<id> {"description":"new desc"}                  → 200 (description changed, name preserved)
//	PATCH /api/v1/items/<id> {"name":"renamed","description":"new desc"} → 200 (both changed)
//	PATCH /api/v1/items/<id>                                             → 400 TypeRequired (empty body)
//	PATCH /api/v1/items/<id> {}                                          → 400 TypeRequired (no fields)
//	PATCH /api/v1/items/<id> {"desc":"new desc"}                         → 400 TypeBadRequest unknown field "desc"
//	PATCH /api/v1/items/missing {"name":"x"}                             → 404 TypeNotFound
//
// Decoding is strict (decodeJSONStrict), matching Create and Update: unknown
// fields are rejected rather than silently ignored.
func (h ItemHandler) Patch(w http.ResponseWriter, r *http.Request) {
	id := router.Param(r, "id")

	var req itemWriteReq
	if err := decodeJSONStrict(r, &req); err != nil {
		if errors.Is(err, io.EOF) {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeRequired).
				Code(codeItemPatchBodyRequired).
				Message("request body is required").
				Build()))
			return
		}
		if field := unknownFieldName(err); field != "" {
			logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeBadRequest).
				Code(codeItemPatchUnknownField).
				Detail("field", field).
				Message("request body contains an unknown field").
				Build()))
			return
		}
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(codeItemPatchInvalidJSON).
			Message("request body must be valid JSON").
			Build()))
		return
	}

	if req.Name == "" && req.Description == "" {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Code(codeItemPatchNoFields).
			Message("at least one of name or description is required").
			Build()))
		return
	}

	updated, ok, err := h.Service.Patch(r.Context(), id, req.Name, req.Description)
	if err != nil {
		requestID := contract.RequestIDFromContext(r.Context())
		h.Logger.Error("item patch failed", plumelog.Fields{
			"request_id": requestID,
			"item_id":    id,
			"err":        err.Error(),
		})
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(codeItemPatchFailed).
			Detail("request_id", requestID).
			Message("failed to patch item").
			Build()))
		return
	}
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(codeItemNotFound).
			Detail("id", id).
			Message("item not found").
			Build()))
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, updated, nil))
}

// Delete handles DELETE /api/v1/items/:id.
// A successful delete returns 204 No Content with no body.
//
//	DELETE /api/v1/items/<id>    → 204
//	DELETE /api/v1/items/missing → 404 TypeNotFound
func (h ItemHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := router.Param(r, "id")
	ok, err := h.Service.Delete(r.Context(), id)
	if err != nil {
		requestID := contract.RequestIDFromContext(r.Context())
		h.Logger.Error("item delete failed", plumelog.Fields{
			"request_id": requestID,
			"item_id":    id,
			"err":        err.Error(),
		})
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Code(codeItemDeleteFailed).
			Detail("request_id", requestID).
			Message("failed to delete item").
			Build()))
		return
	}
	if !ok {
		logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Code(codeItemNotFound).
			Detail("id", id).
			Message("item not found").
			Build()))
		return
	}
	// WriteResponse with 204 writes only the status line — contract.writeJSON
	// skips the body for statusDisallowsBody statuses (1xx, 204, 304).
	// Using WriteResponse keeps the response path consistent with all other handlers.
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusNoContent, nil, nil))
}
