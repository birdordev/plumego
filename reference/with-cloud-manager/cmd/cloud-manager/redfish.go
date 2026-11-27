package main

import (
	"context"
	"database/sql"
	"net/http"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/redfish"
)

type redfishProfile struct {
	Ref string `json:"credential_ref"`
	redfish.Config
}

func runRedfish(ctx context.Context, db *sql.DB, args []string, path string, transport http.RoundTripper) error {
	var profile redfishProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return err
	}
	if len(args) == 5 {
		region := args[3]
		if region == "-" {
			region = ""
		}
		if !redfish.SupportsInventory(region, provider.Kind(args[4])) {
			return inventory.ErrInvalidQuery
		}
		if region != "" && region != profile.Site {
			return &provider.Failure{Kind: provider.Unauthorized}
		}
	}
	var client *redfish.Client
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	return runInventoryCommand(ctx, db, args, provider.Redfish, profile.Ref, func(connectCtx context.Context) (inventoryReader, error) {
		next, err := redfish.Connect(connectCtx, profile.Config, transport)
		if err != nil {
			return nil, err
		}
		client = next
		if len(args) == 3 {
			binding, err := identity.NewStore(db).CloudAccountBinding(connectCtx, args[1], args[2])
			if err != nil || binding.Provider != provider.Redfish || binding.CredentialRef != profile.Ref ||
				(binding.RemoteIdentity != "" && binding.RemoteIdentity != client.Identity()) {
				return nil, &provider.Failure{Kind: provider.Unauthorized}
			}
			scope := provider.Scope{Provider: provider.Redfish, RemoteIdentity: client.Identity(), Region: profile.Site, Kind: provider.BareMetal}
			if _, err := client.Discover(connectCtx, scope, ""); err != nil {
				return nil, err
			}
		}
		return client, nil
	})
}
