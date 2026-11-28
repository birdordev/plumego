package cloud

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func RunIdleCommand(ctx context.Context, db *sql.DB, args []string, profilesPath string, transport http.RoundTripper) error {
	store := controller.NewStore(db)
	if args[0] == "idle-schedule" {
		var interval time.Duration
		if args[5] != "off" {
			var err error
			interval, err = time.ParseDuration(args[5])
			if err != nil || interval <= 0 {
				return errors.New("invalid idle schedule interval")
			}
		}
		key := inventory.Key{Tenant: args[1], Account: args[2], Region: args[3], Kind: provider.Instance}
		if err := store.ConfigureIdle(ctx, key, args[4], interval); err != nil {
			return errors.New("idle analysis scheduling failed")
		}
		return nil
	}
	var profiles map[string]string
	if err := LoadPrivateProfile(profilesPath, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("idle controller needs a credential reference map")
	}
	if err := store.RunIdleAnalysis(ctx, idleScheduleExecutor(inventory.NewStore(db), profiles, transport)); err != nil {
		return errors.New("idle analysis controller storage unavailable")
	}
	return nil
}

func idleScheduleExecutor(store inventory.Store, profiles map[string]string, transport http.RoundTripper) controller.Execute {
	return func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		if task.ScheduleKind != controller.IdleScheduleKind || profiles[task.CredentialRef] == "" {
			return nil, errors.New("idle analysis profile unavailable")
		}
		return store.PrepareIdleAnalysis(ctx, task.Key, task.TargetID, task.CredentialRef, task.CredentialRevision,
			func(ctx context.Context, scope provider.Scope, id string, window provider.MetricWindow) (provider.InstanceUtilization, error) {
				var profile PublicCloudProfile
				if err := LoadPrivateProfile(profiles[task.CredentialRef], &profile); err != nil {
					return provider.InstanceUtilization{}, err
				}
				if profile.Ref != task.CredentialRef || scope.Provider != task.Provider {
					return provider.InstanceUtilization{}, &provider.Failure{Kind: provider.Unauthorized}
				}
				client, err := publiccloud.Connect(ctx, task.Provider, profile.Config, transport)
				if err != nil {
					return provider.InstanceUtilization{}, err
				}
				return client.InstanceUtilization(ctx, scope, id, window)
			})
	}
}
