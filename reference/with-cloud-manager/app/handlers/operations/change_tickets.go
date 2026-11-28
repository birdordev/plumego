package operationhttp

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

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const TicketPolicyRoute = transport.GovernanceRoute + "/ticket-policy"
const ChangeTicketRoute = transport.GovernanceRoute + "/change-tickets/:operation"
const TicketPolicyInputLimit = 16 << 10
const TicketEvidenceInputLimit = 4096

func TicketPolicyHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		policy, err := applyTicketPolicyRequest(ctx, r, store, token, scope)
		if err != nil {
			writeTicketError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(policy.Revision, 10)))
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusCreated, policy, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, policy, nil)
		}
		if err != nil {
			logger.Warn("change ticket policy response write failed", plumelog.Fields{})
		}
	})
}

func applyTicketPolicyRequest(ctx context.Context, r *http.Request, store operations.Store, token string, scope identity.Scope) (operations.TicketPolicy, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		var revision int64
		if r.URL.RawQuery != "" {
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(r.URL.RawQuery) > 32 || len(values) != 1 || len(values["revision"]) != 1 {
				return operations.TicketPolicy{}, operations.ErrInvalid
			}
			revision, err = strconv.ParseInt(values.Get("revision"), 10, 64)
			if err != nil || revision < 1 {
				return operations.TicketPolicy{}, operations.ErrInvalid
			}
		}
		return store.GetTicketPolicy(ctx, token, scope, revision)
	}
	if r.URL.RawQuery != "" {
		return operations.TicketPolicy{}, operations.ErrInvalid
	}
	var expected int64
	if r.Method == http.MethodPut {
		var ok bool
		if expected, ok = transport.AccountRevision(r); !ok {
			return operations.TicketPolicy{}, operations.ErrTicketStale
		}
	} else if len(r.Header.Values("If-Match")) > 0 {
		return operations.TicketPolicy{}, operations.ErrInvalid
	}
	var input operations.TicketPolicyInput
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: TicketPolicyInputLimit}); err != nil {
		return operations.TicketPolicy{}, err
	}
	return store.PublishTicketPolicy(ctx, token, scope, expected, input)
}

func ChangeTicketHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok || (r.Method == http.MethodPost && token == "") {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		if r.URL.RawQuery != "" {
			writeTicketError(w, r, operations.ErrInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		id := router.Param(r, "operation")
		var ticket operations.ChangeTicket
		var err error
		if r.Method == http.MethodPost {
			var input operations.TicketEvidenceInput
			if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: TicketEvidenceInputLimit}); err != nil {
				writeTicketError(w, r, err, logger)
				return
			}
			ticket, err = store.RecordChangeTicket(ctx, token, scope, id, input)
		} else {
			ticket, err = store.GetChangeTicket(ctx, token, scope, id)
		}
		if err != nil {
			writeTicketError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, ticket, nil); err != nil {
			logger.Warn("change ticket response write failed", plumelog.Fields{})
		}
	})
}

func writeTicketError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	switch {
	case errors.Is(err, operations.ErrInvalid):
		err = contract.NewErrorBuilder().Type(contract.TypeBadRequest).Message("invalid change ticket request").Build()
	case errors.Is(err, operations.ErrTicketNotFound):
		err = contract.NewErrorBuilder().Type(contract.TypeNotFound).Message("change ticket policy is unavailable").Build()
	case errors.Is(err, operations.ErrTicketStale):
		err = contract.NewErrorBuilder().Type(contract.TypePreconditionFailed).Message("current change ticket policy ETag required").Build()
	case errors.Is(err, operations.ErrConflict):
		err = contract.NewErrorBuilder().Type(contract.TypeConflict).Message("change ticket conflicts with current state").Build()
	}
	transport.WriteAccountError(w, r, err, logger)
}
