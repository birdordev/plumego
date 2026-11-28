package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/provider"
)

const assetsUsage = `usage: cloud-manager assets {list,changes,topology,export} TENANT PROJECT
  [--account ACCOUNT] [--region REGION] [--kind KIND] [--q TEXT]
  list, changes and topology also accept --limit COUNT and --cursor CURSOR
  cloud-manager assets database-backups TENANT PROJECT ASSET --account ACCOUNT --region REGION [--limit COUNT] [--offset OFFSET --observed-at TIME]
  cloud-manager assets database-restore-windows TENANT PROJECT ASSET --account ACCOUNT --region REGION`

func RunAssetsClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(assetsUsage)
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return usage
	}
	if args[0] == "database-backups" || args[0] == "database-restore-windows" {
		return runDatabaseSnapshotClient(ctx, args, address, token, output, transport)
	}
	command := args[0]
	maximum, cursorLimit := 200, 4096
	switch command {
	case "list", "export":
	case "changes":
		cursorLimit = 1024
	case "topology":
		maximum = 50
	default:
		return usage
	}
	flags := flag.NewFlagSet("assets "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account, region := flags.String("account", "", ""), flags.String("region", "", "")
	kind, search := flags.String("kind", "", ""), flags.String("q", "", "")
	var limit int
	var cursor string
	export := command == "export"
	if !export {
		flags.IntVar(&limit, "limit", 50, "")
		flags.StringVar(&cursor, "cursor", "", "")
	}
	if flags.Parse(args[3:]) != nil || flags.NArg() != 0 ||
		(*kind != "" && !provider.Kind(*kind).Valid()) ||
		(!export && (limit < 1 || limit > maximum || len(cursor) > cursorLimit)) {
		return usage
	}
	query := make(url.Values)
	for key, value := range map[string]string{"account": *account, "region": *region, "kind": *kind, "q": *search} {
		if len(value) > 256 || strings.ContainsRune(value, 0) {
			return usage
		}
		if value != "" {
			query.Set(key, value)
		}
	}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "assets")
	if command != "list" {
		target = target.JoinPath(command)
	}
	target.RawQuery = query.Encode()
	return projecthttp.Run(ctx, projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK,
		CSV: export, ResponseLimit: 16 << 20}, token, nil, output, transport)
}
