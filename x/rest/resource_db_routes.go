package rest

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// Index handles GET /resource with database query, pagination, and transformation.
func (c *DBResourceController[T]) Index(w http.ResponseWriter, r *http.Request) {
	params := c.ParseQueryParams(r)

	if err := c.Hooks.BeforeList(r.Context(), params); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidRequest).
			Message("list hook rejected request").
			Build())
		return
	}

	results, total, err := c.repository.FindAll(r.Context(), params)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to fetch records").
			Build())
		return
	}

	transformedResults, err := c.Transformer.TransformCollection(r.Context(), results)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to transform results").
			Build())
		return
	}

	if err := c.Hooks.AfterList(r.Context(), params, transformedResults); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("post-list hook failed").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, transformedResults, map[string]any{
		"pagination": NewPaginationMeta(params.Page, params.PageSize, total),
	})
}

// Show handles GET /resource/:id.
func (c *DBResourceController[T]) Show(w http.ResponseWriter, r *http.Request) {
	id := c.ParamExtractor.GetID(r)
	if id == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Message("id is required").
			Build())
		return
	}

	result, err := c.repository.FindByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeNotFound).
				Message("record not found").
				Build())
			return
		}
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to fetch record").
			Build())
		return
	}

	transformedResult, err := c.Transformer.Transform(r.Context(), result)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to transform result").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, transformedResult, nil)
}

// Create handles POST /resource.
func (c *DBResourceController[T]) Create(w http.ResponseWriter, r *http.Request) {
	var data T
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidRequest).
			Message("invalid request body").
			Build())
		return
	}

	if c.validator != nil {
		if err := c.validator.Validate(data); err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInvalidRequest).
				Code(contract.CodeValidationError).
				Message("validation failed").
				Detail("details", err).
				Build())
			return
		}
	}

	if err := c.Hooks.BeforeCreate(r.Context(), &data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("create hook rejected request").
			Build())
		return
	}

	if err := c.repository.Create(r.Context(), &data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to create record").
			Build())
		return
	}

	if err := c.Hooks.AfterCreate(r.Context(), &data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("post-create hook failed").
			Build())
		return
	}

	transformedResult, err := c.Transformer.Transform(r.Context(), &data)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to transform result").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusCreated, transformedResult, nil)
}

// Update handles PUT /resource/:id.
func (c *DBResourceController[T]) Update(w http.ResponseWriter, r *http.Request) {
	id := c.ParamExtractor.GetID(r)
	if id == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Message("id is required").
			Build())
		return
	}

	var data T
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidRequest).
			Message("invalid request body").
			Build())
		return
	}

	if c.validator != nil {
		if err := c.validator.Validate(data); err != nil {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeInvalidRequest).
				Code(contract.CodeValidationError).
				Message("validation failed").
				Detail("details", err).
				Build())
			return
		}
	}

	if err := c.Hooks.BeforeUpdate(r.Context(), id, &data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("update hook rejected request").
			Build())
		return
	}

	if err := c.repository.Update(r.Context(), id, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeNotFound).
				Message("record not found").
				Build())
			return
		}
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to update record").
			Build())
		return
	}

	if err := c.Hooks.AfterUpdate(r.Context(), id, &data); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("post-update hook failed").
			Build())
		return
	}

	transformedResult, err := c.Transformer.Transform(r.Context(), &data)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to transform result").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, transformedResult, nil)
}

// Delete handles DELETE /resource/:id.
func (c *DBResourceController[T]) Delete(w http.ResponseWriter, r *http.Request) {
	id := c.ParamExtractor.GetID(r)
	if id == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Message("id is required").
			Build())
		return
	}

	if err := c.Hooks.BeforeDelete(r.Context(), id); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("delete hook rejected request").
			Build())
		return
	}

	if err := c.repository.Delete(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = contract.WriteError(w, r, contract.NewErrorBuilder().
				Type(contract.TypeNotFound).
				Message("record not found").
				Build())
			return
		}
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("failed to delete record").
			Build())
		return
	}

	if err := c.Hooks.AfterDelete(r.Context(), id); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeInternal).
			Message("post-delete hook failed").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusNoContent, nil, nil)
}
