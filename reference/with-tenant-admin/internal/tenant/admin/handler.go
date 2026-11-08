package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
)

// Handler serves the tenant admin endpoints.
// Logger must not be nil; pass app.Core.Logger() from app.New.
type Handler struct {
	store  *InMemoryStore
	Logger plumelog.StructuredLogger
}

type CreateTenantRequest struct {
	Name string `json:"name"`
}

func NewHandler(store *InMemoryStore, logger plumelog.StructuredLogger) *Handler {
	if store == nil {
		store = NewInMemoryStore()
	}
	return &Handler{store: store, Logger: logger}
}

func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, contract.TypeValidation, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		h.writeError(w, r, contract.TypeValidation, "tenant name is required")
		return
	}

	record, err := h.store.Create(r.Context(), TenantRecord{
		ID:        newTenantID(),
		Name:      name,
		Status:    StatusActive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "create tenant failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusCreated, record, nil))
}

func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	record, err := h.store.Get(r.Context(), router.Param(r, "id"))
	if errors.Is(err, ErrNotFound) {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "get tenant failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, record, nil))
}

func (h *Handler) SuspendTenant(w http.ResponseWriter, r *http.Request) {
	record, err := h.store.Suspend(r.Context(), router.Param(r, "id"))
	if errors.Is(err, ErrNotFound) {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "suspend tenant failed")
		return
	}
	logWriteErr(h.Logger, contract.WriteResponse(w, r, http.StatusOK, record, nil))
}

func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	err := h.store.Delete(r.Context(), router.Param(r, "id"))
	if errors.Is(err, ErrNotFound) {
		h.writeError(w, r, contract.TypeNotFound, "tenant not found")
		return
	}
	if err != nil {
		h.writeError(w, r, contract.TypeInternal, "delete tenant failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, typ contract.ErrorType, message string) {
	logWriteErr(h.Logger, contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(typ).
		Message(message).
		Build()))
}

func logWriteErr(logger plumelog.StructuredLogger, err error) {
	if err == nil || logger == nil {
		return
	}
	logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
}

func newTenantID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "tenant-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
