package devserver

import (
	"context"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// BuilderAPI defines the builder methods exposed to commands and tests.
type BuilderAPI interface {
	HasCustomBuild() bool
	SetCustomBuild(cmd string, args []string)
	Build(ctx context.Context) error
	Clean() error
	OutputPath() string
	Verify() error
}

// RunnerAPI defines the runner methods exposed to commands and tests.
type RunnerAPI interface {
	HasCustomCommand() bool
	SetCustomCommand(cmd string, args []string)
	SetOutputPassthrough(enabled bool)
	SetEnv(key, value string)
	IsRunning() bool
	Start(ctx context.Context) error
	Stop() error
	Restart(ctx context.Context) error
}

// DashboardAPI defines the methods used by the dev command.
type DashboardAPI interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	BuildAndRun(ctx context.Context) error
	Rebuild(ctx context.Context) error
	PublishEvent(eventType string, data any)
	GetPubSub() *pubsub.InProcBroker
	GetBuilder() BuilderAPI
	GetRunner() RunnerAPI
}
