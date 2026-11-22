package app

import (
	"context"
	"errors"
	"testing"
)

func TestShutdownAttemptsBothTransports(t *testing.T) {
	httpErr, wsErr := errors.New("HTTP failed"), errors.New("WebSocket failed")
	got := shutdownTransports(context.Background(),
		func(context.Context) error { return httpErr },
		func(context.Context) error { return wsErr },
	)
	if !errors.Is(got, httpErr) || !errors.Is(got, wsErr) {
		t.Fatalf("shutdown lost an error: %v", got)
	}
}
