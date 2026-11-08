package httputil

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

// WriteTransportError writes transport errors using the canonical contract helper.
func WriteTransportError(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	details map[string]any,
) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(errorTypeForStatus(status)).
		Code(code).
		Message(message).
		Details(details).
		Build())
}

func errorTypeForStatus(status int) contract.ErrorType {
	switch status {
	case http.StatusRequestEntityTooLarge:
		return contract.TypePayloadTooLarge
	case http.StatusTooManyRequests:
		return contract.TypeRateLimited
	case http.StatusRequestTimeout:
		return contract.TypeTimeout
	case http.StatusBadGateway:
		return contract.TypeBadGateway
	case http.StatusGatewayTimeout:
		return contract.TypeGatewayTimeout
	case http.StatusServiceUnavailable:
		return contract.TypeUnavailable
	case http.StatusInternalServerError:
		return contract.TypeInternal
	default:
		if status >= http.StatusInternalServerError {
			return contract.TypeInternal
		}
		return contract.TypeBadRequest
	}
}
