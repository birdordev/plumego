package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
	"with-cloud-manager/internal/provider/publiccloud"
	"with-cloud-manager/internal/provider/redfish"
)

func runWorker(ctx context.Context, db *sql.DB, manifest, mode string) error {
	store, err := configuredOperationWorkerStore(db, mode)
	if err != nil {
		return err
	}
	var profiles map[string]string
	if err := loadPrivateProfile(manifest, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("worker needs a credential reference map")
	}
	if err := store.RunWorkerWithWorkloadScale(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		return connectOperator(ctx, binding, profiles[binding.CredentialRef])
	}, func(ctx context.Context, binding identity.CloudAccountBinding, region, cluster string) (provider.WorkloadScaler, error) {
		return connectWorkloadScaler(ctx, binding, profiles[binding.CredentialRef], region, cluster)
	}); err != nil {
		return errors.New("instance operation worker stopped")
	}
	return nil
}

func configuredOperationWorkerStore(db *sql.DB, mode string) (operations.Store, error) {
	switch mode {
	case "", "dispatch":
		return operations.NewStore(db), nil
	case "observe-only":
		return operations.NewStore(db).WithObservationOnly(), nil
	default:
		return operations.Store{}, errors.New("CLOUD_MANAGER_WORKER_MODE must be dispatch or observe-only")
	}
}

func connectOperator(ctx context.Context, binding identity.CloudAccountBinding, path string) (provider.InstanceOperator, error) {
	switch binding.Provider {
	case awsec2.Name:
		var profile awsProfile
		if err := loadPrivateProfile(path, &profile); err != nil {
			return nil, err
		}
		if profile.Ref != binding.CredentialRef {
			return nil, identity.ErrForbidden
		}
		client, err := awsec2.Connect(ctx, profile.Config)
		if err != nil {
			return nil, err
		}
		if client.Identity() != binding.RemoteIdentity {
			return nil, identity.ErrForbidden
		}
		return client, nil
	case openstack.Name:
		var profile openStackProfile
		if err := loadPrivateProfile(path, &profile); err != nil {
			return nil, err
		}
		if profile.Ref != binding.CredentialRef {
			return nil, identity.ErrForbidden
		}
		client, err := openstack.Connect(ctx, profile.Config, nil)
		if err != nil {
			return nil, err
		}
		if client.Identity() != binding.RemoteIdentity {
			return nil, identity.ErrForbidden
		}
		return client, nil
	case provider.Redfish:
		return connectRedfishPower(ctx, binding, path)
	case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
		return connectPublicCloudOperator(ctx, binding, path, nil)
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
}

func connectPublicCloudOperator(ctx context.Context, binding identity.CloudAccountBinding, path string, transport http.RoundTripper) (provider.InstanceOperator, error) {
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return nil, err
	}
	if profile.Ref != binding.CredentialRef {
		return nil, identity.ErrForbidden
	}
	client, err := publiccloud.Connect(ctx, binding.Provider, profile.Config, transport)
	if provider.IsFailure(err, provider.Unauthorized) {
		return nil, identity.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	if client.Identity() != binding.RemoteIdentity {
		return nil, identity.ErrForbidden
	}
	return client.InstanceOperator(), nil
}

func connectRedfishPower(ctx context.Context, binding identity.CloudAccountBinding, path string) (provider.InstanceOperator, error) {
	var profile redfishProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return nil, err
	}
	if profile.Ref != binding.CredentialRef || binding.RemoteIdentity != profile.Config.Identity() {
		return nil, identity.ErrForbidden
	}
	client, err := redfish.Connect(ctx, profile.Config, nil)
	if provider.IsFailure(err, provider.Unauthorized) {
		return nil, identity.ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	return redfish.PowerOperator{Client: client}, nil
}
