package assets

import (
	"context"
	"errors"
	"net/http"

	plumelog "github.com/birdordev/plumego/log"

	operationhttp "with-cloud-manager/app/handlers/operations"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

const InstanceResizeOptionsRoute = operationhttp.InstancesRoute + "/resize-options"

func InstanceResizeOptionsHandler(store inventory.Store, refresh bool, logger plumelog.StructuredLogger) http.Handler {
	return operationhttp.AssetOperationPreviewHandler(func(ctx context.Context, token string, project identity.Scope, account, region, id string) (inventory.ResizeOptions, error) {
		options, err := store.InstanceResizeOptions(ctx, token, project, account, region, id, refresh)
		switch {
		case errors.Is(err, inventory.ErrInvalidQuery):
			err = operations.ErrInvalid
		case errors.Is(err, identity.ErrAccountStale):
			err = operationhttp.InstanceResizeRevisionError()
		}
		return options, err
	}, logger)
}
