package transport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/security/authn"

	"with-cloud-manager/internal/identity"
)

func AccountRevision(r *http.Request) (int64, bool) {
	return MinimumAccountRevision(r, 1)
}

func MinimumAccountRevision(r *http.Request, minimum int64) (int64, bool) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 || !strings.HasPrefix(values[0], `"`) || !strings.HasSuffix(values[0], `"`) || len(values[0]) < 3 {
		return 0, false
	}
	revision, err := strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	return revision, err == nil && revision >= minimum && strconv.Quote(strconv.FormatInt(revision, 10)) == values[0]
}

func WriteAccountError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
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

func RequestCredential(r *http.Request) (string, bool) {
	if len(r.Header.Values("Authorization")) == 1 {
		value := authn.ExtractBearerToken(r)
		return value, value != ""
	}
	if len(r.Header.Values("Authorization")) == 0 {
		_, ok := identity.BrowserSessionFromContext(r.Context())
		return "", ok
	}
	return "", false
}
