package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
)

const HeaderAdminToken = "X-Admin-Token"

func RequireAdminToken(adminToken string, logger plumelog.StructuredLogger) func(http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(strings.TrimSpace(adminToken)))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := sha256.Sum256([]byte(strings.TrimSpace(r.Header.Get(HeaderAdminToken))))
			if !hmac.Equal(got[:], expected[:]) {
				logWriteErr(logger, contract.WriteError(w, r, contract.NewErrorBuilder().
					Type(contract.TypeUnauthorized).
					Code(contract.CodeUnauthorized).
					Message("admin token is required").
					Build()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func logWriteErr(logger plumelog.StructuredLogger, err error) {
	if err == nil || logger == nil {
		return
	}
	logger.Warn("write response failed", plumelog.Fields{"error": err.Error()})
}
