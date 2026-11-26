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

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const governanceRoute = "/v1/tenants/:tenant/projects/:project/governance"
const tagPolicyRoute = governanceRoute + "/tag-policy"
const tagComplianceRoute = governanceRoute + "/tag-compliance"
const tagPolicyInputLimit = 128 << 10

func tagPolicyHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeTagPolicyError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := inventory.TagPolicyScope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		policy, err := applyTagPolicyRequest(ctx, r, store, token, scope)
		if err != nil {
			writeTagPolicyError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(policy.Revision, 10)))
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusCreated, policy, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, policy, nil)
		}
		if err != nil {
			logger.Warn("tag policy response write failed", plumelog.Fields{})
		}
	})
}

func applyTagPolicyRequest(ctx context.Context, r *http.Request, store inventory.Store, token string, scope inventory.TagPolicyScope) (inventory.TagPolicy, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		var revision int64
		if r.URL.RawQuery != "" {
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(r.URL.RawQuery) > 32 || len(values) != 1 || len(values["revision"]) != 1 {
				return inventory.TagPolicy{}, inventory.ErrInvalidQuery
			}
			revision, err = strconv.ParseInt(values.Get("revision"), 10, 64)
			if err != nil || revision < 1 {
				return inventory.TagPolicy{}, inventory.ErrInvalidQuery
			}
		}
		return store.GetTagPolicy(ctx, token, scope, revision)
	}
	if r.URL.RawQuery != "" {
		return inventory.TagPolicy{}, inventory.ErrInvalidQuery
	}
	var expected int64
	if r.Method == http.MethodPut {
		var ok bool
		if expected, ok = accountRevision(r); !ok {
			return inventory.TagPolicy{}, inventory.ErrTagPolicyStale
		}
	} else if len(r.Header.Values("If-Match")) > 0 {
		return inventory.TagPolicy{}, inventory.ErrInvalidQuery
	}
	var input inventory.TagPolicyInput
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: tagPolicyInputLimit}); err != nil {
		return inventory.TagPolicy{}, err
	}
	return store.SaveTagPolicy(ctx, token, scope, expected, input)
}

func writeTagPolicyError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	apiErr, ok := contract.AsAPIError(err)
	if !ok {
		kind, message := contract.TypeUnavailable, "tag policy storage is unavailable"
		switch {
		case errors.Is(err, identity.ErrUnauthenticated):
			w.Header().Set("WWW-Authenticate", `Bearer realm="cloud-manager"`)
			kind, message = contract.TypeUnauthorized, "authentication required"
		case errors.Is(err, identity.ErrForbidden):
			kind, message = contract.TypeForbidden, "project access is unavailable"
		case errors.Is(err, inventory.ErrInvalidQuery):
			kind, message = contract.TypeBadRequest, "invalid tag policy request"
		case errors.Is(err, inventory.ErrTagPolicyNotFound):
			kind, message = contract.TypeNotFound, "tag policy is unavailable"
		case errors.Is(err, inventory.ErrTagPolicyExists):
			kind, message = contract.TypeConflict, "tag policy already exists"
		case errors.Is(err, inventory.ErrTagPolicyStale):
			kind, message = contract.TypePreconditionFailed, "current tag policy ETag required"
		}
		apiErr = contract.NewErrorBuilder().Type(kind).Message(message).Build()
	}
	if err := contract.WriteError(w, r, apiErr); err != nil {
		logger.Warn("tag policy error response write failed", plumelog.Fields{})
	}
}

func tagComplianceHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveInventory(parseTagComplianceQuery, store.TagCompliance,
		func(w http.ResponseWriter, r *http.Request, report inventory.TagCompliance) error {
			return contract.WriteResponse(w, r, http.StatusOK, report, nil)
		}, logger)
}

func parseTagComplianceQuery(r *http.Request) (inventory.TagComplianceQuery, error) {
	query, state, err := parseGovernanceReportQuery(r, true)
	return inventory.TagComplianceQuery{Query: query, State: state}, err
}

func parseGovernanceReportQuery(r *http.Request, allowKind bool) (inventory.Query, string, error) {
	q := inventory.Query{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), Limit: 50}
	var state string
	if len(r.URL.RawQuery) > 8192 {
		return q, "", inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, "", inventory.ErrInvalidQuery
	}
	for name, entries := range values {
		if len(entries) != 1 {
			return q, "", inventory.ErrInvalidQuery
		}
		value := entries[0]
		switch name {
		case "account":
			q.Account = value
		case "region":
			q.Region = value
		case "kind":
			if !allowKind {
				return q, "", inventory.ErrInvalidQuery
			}
			q.Kind = provider.Kind(value)
		case "state":
			state = value
		case "cursor":
			q.Cursor = value
		case "limit":
			if q.Limit, err = strconv.Atoi(value); err != nil {
				return q, "", inventory.ErrInvalidQuery
			}
		default:
			return q, "", inventory.ErrInvalidQuery
		}
	}
	return q, state, nil
}
