package scheduler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/birdordev/plumego/log"
)

type diagnosticTestError struct {
	Secret string
}

func (e diagnosticTestError) Error() string { return e.Secret }

func TestAdminAndSchedulerLogsHideFailureDetails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const secret = "credential-injected-by-handler"
		var output bytes.Buffer
		logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatJSON, Output: &output, ErrorOutput: &output})
		s := New(WithWorkers(1), WithDeadLetterQueue(8), WithLogger(logger))
		s.Start()
		if _, err := s.Delay("failed", 0, func(context.Context) error {
			return diagnosticTestError{Secret: secret}
		}, WithRetryPolicy(RetryFixed(2, 0))); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Delay("panicked", 0, func(context.Context) error {
			panic(secret)
		}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := s.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		admin := NewAdminHandler(s)
		for _, tc := range []struct{ path, code string }{
			{"/scheduler/jobs", "scheduler_failure"},
			{"/scheduler/jobs/failed", "scheduler_failure"},
			{"/scheduler/dlq", "scheduler_failure"},
			{"/scheduler/dlq/failed", "scheduler_failure"},
			{"/scheduler/jobs/panicked", "task_panicked"},
			{"/scheduler/dlq/panicked", "task_panicked"},
		} {
			response := doAdminReq(admin, http.MethodGet, tc.path)
			body := response.Body.String()
			if response.Code != http.StatusOK || !strings.Contains(body, tc.code) || strings.Contains(body, secret) {
				t.Errorf("%s = %d %s", tc.path, response.Code, body)
			}
		}
		if logs := output.String(); strings.Contains(logs, secret) || !strings.Contains(logs, "scheduler_failure") {
			t.Fatalf("unexpected scheduler logs: %s", logs)
		}
		status, _ := s.Status("failed")
		var cause diagnosticTestError
		if !errors.As(status.LastError, &cause) || cause.Secret != secret {
			t.Fatal("in-process cause was lost")
		}
	})
}
