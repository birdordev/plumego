package main

import (
	"context"
	"errors"
	"net/http"

	plumelog "github.com/birdordev/plumego/log"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const instanceResizeOptionsRoute = instancesRoute + "/resize-options"

func instanceResizeOptionsHandler(store inventory.Store, refresh bool, logger plumelog.StructuredLogger) http.Handler {
	return assetOperationPreviewHandler(func(ctx context.Context, token string, project identity.Scope, account, region, id string) (inventory.ResizeOptions, error) {
		options, err := store.InstanceResizeOptions(ctx, token, project, account, region, id, refresh)
		switch {
		case errors.Is(err, inventory.ErrInvalidQuery):
			err = operations.ErrInvalid
		case errors.Is(err, identity.ErrAccountStale):
			err = instanceResizeRevisionError()
		}
		return options, err
	}, logger)
}

func instanceResizeOptionsReader(profiles map[string]string, transport http.RoundTripper) inventory.ReadResizeOptions {
	return func(ctx context.Context, binding identity.CloudAccountBinding, scope provider.Scope, id, current string) (provider.InstanceResizeOptions, error) {
		var profile publicCloudProfile
		if err := loadPrivateProfile(profiles[binding.CredentialRef], &profile); err != nil {
			return provider.InstanceResizeOptions{}, err
		}
		if profile.Ref != binding.CredentialRef {
			return provider.InstanceResizeOptions{}, identity.ErrForbidden
		}
		client, err := publiccloud.Connect(ctx, binding.Provider, profile.Config, transport)
		if err != nil {
			return provider.InstanceResizeOptions{}, err
		}
		return client.InstanceResizeOptions(ctx, scope, id, current)
	}
}
