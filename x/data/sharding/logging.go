package sharding

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/log"
)

// LoggingRouter wraps a router with structured logging for database sharding operations.
// It uses the plumego/log/log.StructuredLogger interface for flexible logging backends.
type LoggingRouter struct {
	router *Router
	logger log.StructuredLogger
}

// NewLoggingRouter creates a new logging router with optional logger.
// If logger is nil, it creates a JSON logger through the canonical NewLogger path.
func NewLoggingRouter(router *Router, logger log.StructuredLogger) *LoggingRouter {
	if logger == nil {
		logger = log.NewLogger(log.LoggerConfig{
			Format: log.LoggerFormatJSON,
			Level:  log.INFO,
			Fields: log.Fields{
				"component": "sharding",
			},
		})
	}

	return &LoggingRouter{
		router: router,
		logger: logger,
	}
}

// Logger returns the underlying logger instance.
func (lr *LoggingRouter) Logger() log.StructuredLogger {
	return lr.logger
}

// Router returns the underlying router instance.
func (lr *LoggingRouter) Router() *Router {
	return lr.router
}

func safeLogFields(fields log.Fields) log.Fields {
	merged := log.Fields{}
	for k, v := range fields {
		merged[k] = v
	}
	return merged
}

// LogQuery logs query execution with safe SQL metadata only.
func (lr *LoggingRouter) LogQuery(ctx context.Context, query string, shardIndex int, latency time.Duration, err error) {
	fields := log.Fields{
		"operation":          getQueryOperation(query),
		"statement_redacted": true,
		"shard_index":        shardIndex,
		"latency_ms":         latency.Milliseconds(),
	}

	if err != nil {
		fields["error"] = "redacted"
		fields["error_type"] = fmt.Sprintf("%T", err)
		fields["error_redacted"] = true
		lr.logger.ErrorCtx(ctx, "query failed", safeLogFields(fields))
	} else {
		lr.logger.InfoCtx(ctx, "query executed", safeLogFields(fields))
	}
}

// LogShardResolution logs shard resolution without exposing the shard key value.
func (lr *LoggingRouter) LogShardResolution(ctx context.Context, tableName string, shardKey any, shardIndex int) {
	_ = shardKey
	lr.logger.InfoCtx(ctx, "shard resolved", safeLogFields(log.Fields{
		"table":              tableName,
		"shard_key_redacted": true,
		"shard_index":        shardIndex,
	}))
}

// LogCrossShardQuery logs cross-shard queries with safe SQL metadata only.
// These are typically less performant and should be monitored.
func (lr *LoggingRouter) LogCrossShardQuery(ctx context.Context, query string, policy string) {
	lr.logger.WarnCtx(ctx, "cross-shard query", safeLogFields(log.Fields{
		"operation":          getQueryOperation(query),
		"statement_redacted": true,
		"policy":             policy,
	}))
}

// LogRewrite logs SQL rewriting without exposing raw SQL text.
func (lr *LoggingRouter) LogRewrite(ctx context.Context, original string, rewritten string, cached bool) {
	_ = rewritten
	lr.logger.InfoCtx(ctx, "SQL rewritten", safeLogFields(log.Fields{
		"operation":          getQueryOperation(original),
		"statement_redacted": true,
		"cached":             cached,
	}))
}
