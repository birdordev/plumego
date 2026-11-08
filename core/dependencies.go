package core

import (
	"github.com/birdordev/plumego/log"
)

// AppDependencies carries explicit constructor dependencies for App.
type AppDependencies struct {
	// Logger receives request and lifecycle-adjacent logs; nil uses a discard logger.
	Logger log.StructuredLogger
}

func resolveLogger(dependencies AppDependencies) log.StructuredLogger {
	if dependencies.Logger == nil {
		return log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard})
	}
	return dependencies.Logger
}
