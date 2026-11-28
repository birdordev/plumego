package cloud

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/vsphere"
)

type VSphereProfile struct {
	Ref string `json:"credential_ref"`
	vsphere.Config
}

func RunVSphere(ctx context.Context, db *sql.DB, args []string, path string, transport http.RoundTripper) (err error) {
	var region string
	if len(args) == 5 {
		region = args[3]
		if region == "-" {
			region = ""
		}
		if !vsphere.SupportsInventory(region, provider.Kind(args[4])) {
			return inventory.ErrInvalidQuery
		}
	}
	var profile VSphereProfile
	if err := LoadPrivateProfile(path, &profile); err != nil {
		return err
	}
	if region != "" && !slices.Contains(profile.Datacenters, region) {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	var client *vsphere.Client
	defer func() {
		if client != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			err = errors.Join(err, client.Close(cleanup))
		}
	}()
	return runInventoryCommand(ctx, db, args, provider.VSphere, profile.Ref, func(connectCtx context.Context) (inventoryReader, error) {
		connect, err := vsphere.Connect(connectCtx, profile.Config, transport)
		if err != nil {
			return nil, err
		}
		client = connect
		return client, nil
	})
}
