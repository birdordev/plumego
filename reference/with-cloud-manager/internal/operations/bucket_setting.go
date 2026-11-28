package operations

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/provider"
)

func isBucketSettingMutation(action string) bool {
	return action == setBucketVersioning || action == enableBucketEncryption
}

func (s Store) observeBucketSetting(ctx context.Context, task mq.Task, w work, eventPrefix string, observe func() (bool, error), confirm func(work, time.Time) error) (bool, error) {
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	started, err := s.dbTime(ctx)
	if err != nil {
		return false, err
	}
	found, err := observe()
	if err == nil && found {
		return false, confirm(w, started)
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) ||
		provider.IsFailure(err, provider.Absent) {
		return false, s.transition(ctx, task, w.Status, "needs_review", eventPrefix+"_requires_evidence", "")
	}
	return true, nil
}
