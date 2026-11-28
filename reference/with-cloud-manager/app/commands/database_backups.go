package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/provider"
)

func runDatabaseSnapshotClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(assetsUsage)
	if len(args) < 4 || !models.ValidRequestSegment(args[3]) {
		return usage
	}
	flags := flag.NewFlagSet("assets "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account, region := flags.String("account", "", ""), flags.String("region", "", "")
	var limit, offset int
	var stamp string
	backups := args[0] == "database-backups"
	if backups {
		flags.IntVar(&limit, "limit", 50, "")
		flags.IntVar(&offset, "offset", 0, "")
		flags.StringVar(&stamp, "observed-at", "", "")
	}
	if flags.Parse(args[4:]) != nil || flags.NArg() != 0 || !models.ValidRequestSegment(*account) || !models.ValidRequestSegment(*region) {
		return usage
	}
	params := url.Values{"account": {*account}, "region": {*region}}
	resource := "restore-windows"
	if backups {
		resource = "backups"
		if limit < 1 || limit > 100 || offset < 0 || offset > provider.MaxDatabaseBackups || (offset > 0 && stamp == "") {
			return usage
		}
		params.Set("offset", strconv.Itoa(offset))
		params.Set("limit", strconv.Itoa(limit))
		if stamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				return usage
			}
			params.Set("observed_at", stamp)
		}
	}
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "assets", "databases", url.PathEscape(args[3]), resource)
	target.RawQuery = params.Encode()
	return projecthttp.Run(ctx, projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, token, nil, output, transport)
}
