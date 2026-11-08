package core

import (
	"reflect"
	"testing"

	"github.com/birdordev/plumego/log"
)

func TestAppDependenciesLogger(t *testing.T) {
	logger := log.NewLogger()
	app := New(DefaultConfig(), AppDependencies{Logger: logger})
	if app.logger != logger {
		t.Errorf("expected logger to be set")
	}
	if app.Logger() != logger {
		t.Fatal("expected App.Logger to return the configured logger")
	}
	if app.router == nil {
		t.Fatal("expected app to own a router instance")
	}
}

func TestNewFallsBackToDiscardLogger(t *testing.T) {
	tests := []struct {
		name         string
		dependencies AppDependencies
	}{
		{name: "default dependencies"},
		{name: "explicit nil logger", dependencies: AppDependencies{Logger: nil}},
	}

	wantType := reflect.TypeOf(log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatDiscard}))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New(DefaultConfig(), tt.dependencies)
			if app.logger == nil {
				t.Fatal("expected logger to be initialized")
			}
			if reflect.TypeOf(app.logger) != wantType {
				t.Fatalf("expected discard logger, got %T", app.logger)
			}
		})
	}
}
