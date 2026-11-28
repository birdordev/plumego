package cloud

import (
	"context"
	"net/http"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func instanceResizeOptionsReader(profiles map[string]string, transport http.RoundTripper) inventory.ReadResizeOptions {
	return func(ctx context.Context, binding identity.CloudAccountBinding, scope provider.Scope, id, current string) (provider.InstanceResizeOptions, error) {
		var profile PublicCloudProfile
		if err := LoadPrivateProfile(profiles[binding.CredentialRef], &profile); err != nil {
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
