package accesslog

import (
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
	internaltransport "github.com/birdordev/plumego/internal/httputil"
	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware"
	internaltelemetry "github.com/birdordev/plumego/middleware/internal/telemetry"
)

// ErrNilLogger is returned when access logging is configured without a logger.
var ErrNilLogger = errors.New("accesslog: logger cannot be nil")

// Config controls access-log middleware behavior.
type Config struct {
	Logger log.StructuredLogger
}

// Middleware is the canonical access-log middleware constructor.
func Middleware(config Config) (middleware.Middleware, error) {
	if config.Logger == nil {
		return nil, ErrNilLogger
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			internaltelemetry.WithObserver(next, w, r, func(metricsData internaltelemetry.RequestMetrics, recorder *internaltelemetry.ResponseRecorder, r *http.Request) {
				rc := contract.RequestContextFromContext(r.Context())

				fields := internaltelemetry.MiddlewareLogFields(r, metricsData.Status, metricsData.Duration)
				fields["bytes"] = metricsData.Bytes
				fields["user_agent"] = metricsData.UserAgent
				fields["client_ip"] = internaltransport.ClientIP(r)
				if metricsData.Route != "" {
					fields["route"] = metricsData.Route
				}
				if rc.RouteName != "" {
					fields["route_name"] = rc.RouteName
				}
				if headerSpanID := recorder.Header().Get(internaltelemetry.SpanIDHeader); headerSpanID != "" {
					fields["span_id"] = headerSpanID
				} else if tc, ok := contract.TraceContextFromContext(r.Context()); ok && tc.HasSpanID() {
					fields["span_id"] = tc.SpanID
				}

				config.Logger.WithFields(log.Fields(internaltelemetry.RedactFields(fields))).Info("request completed")
			})
		})
	}, nil
}
