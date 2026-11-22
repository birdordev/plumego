package main

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/store/idempotency"
)

type handlers struct{ repo repository }

func (h handlers) live(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"live": true}, nil)
}

func (h handlers) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.db.PingContext(r.Context()); err != nil {
		writeAPIError(w, r, contract.TypeUnavailable, "database is not ready")
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"ready": true}, nil)
}

func (h handlers) create(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if err := idempotency.ValidateKey(key); err != nil {
		writeAPIError(w, r, contract.TypeBadRequest, "a valid idempotency key is required")
		return
	}
	name, ok := itemName(w, r)
	if !ok {
		return
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	result, err := h.repo.create(r.Context(), key, fingerprint, name, time.Now())
	if errors.Is(err, errIdempotencyConflict) {
		writeAPIError(w, r, contract.TypeConflict, "idempotency key is already in use for a different request")
		return
	}
	if err != nil {
		writeAPIError(w, r, contract.TypeInternal, "could not write item")
		return
	}
	if result.Replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", "/items/"+strconv.FormatInt(result.Item.ID, 10))
	writeItemHeaders(w.Header(), result.Item)
	_ = contract.WriteResponse(w, r, http.StatusCreated, result.Item, nil)
}

func (h handlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	result, err := h.repo.get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, r, contract.TypeNotFound, "item not found")
	} else if err != nil {
		writeAPIError(w, r, contract.TypeInternal, "could not read item")
	} else {
		writeItemHeaders(w.Header(), result)
		_ = contract.WriteResponse(w, r, http.StatusOK, result, nil)
	}
}

func (h handlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	version, ok := expectedVersion(w, r)
	if !ok {
		return
	}
	name, ok := itemName(w, r)
	if !ok {
		return
	}
	result, err := h.repo.update(r.Context(), id, version, name)
	if writeUpdateError(w, r, err) {
		return
	}
	writeItemHeaders(w.Header(), result)
	_ = contract.WriteResponse(w, r, http.StatusOK, result, nil)
}

func (h handlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := itemID(w, r)
	if !ok {
		return
	}
	version, ok := expectedVersion(w, r)
	if !ok || writeUpdateError(w, r, h.repo.delete(r.Context(), id, version)) {
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusNoContent, nil, nil)
}

func itemID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, r, contract.TypeBadRequest, "invalid item id")
		return 0, false
	}
	return id, true
}

func itemName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var input struct {
		Name string `json:"name"`
	}
	if err := contract.DecodeJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
		apiErr, _ := contract.AsAPIError(err)
		_ = contract.WriteError(w, r, apiErr)
		return "", false
	}
	name := strings.TrimSpace(input.Name)
	if len(name) == 0 || len(name) > 128 {
		writeAPIError(w, r, contract.TypeBadRequest, "name must contain between 1 and 128 bytes")
		return "", false
	}
	return name, true
}

// 示例只接受一个强实体标签或 *，在修改数据的同一条 SQL 中检查前置条件。
func expectedVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	tag := r.Header.Get("If-Match")
	if tag == "*" {
		return 0, true
	}
	if len(tag) >= 3 && tag[0] == '"' && tag[len(tag)-1] == '"' {
		version, err := strconv.ParseInt(tag[1:len(tag)-1], 10, 64)
		if err == nil && version > 0 && tag == fmt.Sprintf(`"%d"`, version) {
			return version, true
		}
	}
	writeAPIError(w, r, contract.TypeBadRequest, "one If-Match item tag or * is required")
	return 0, false
}

func writeUpdateError(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, errPrecondition) {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypePreconditionFailed).Message("item no longer matches the requested version").Build())
	} else if err != nil {
		writeAPIError(w, r, contract.TypeInternal, "could not update item")
	}
	return err != nil
}

func writeItemHeaders(header http.Header, result item) {
	header.Set("ETag", fmt.Sprintf(`"%d"`, result.Version))
	header.Set("Cache-Control", "private, no-store")
}

func writeAPIError(w http.ResponseWriter, r *http.Request, typ contract.ErrorType, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().Type(typ).Message(message).Build())
}
