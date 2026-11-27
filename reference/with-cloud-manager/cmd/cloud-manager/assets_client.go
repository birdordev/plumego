package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const assetsUsage = `usage: cloud-manager assets {list,changes,topology,export} TENANT PROJECT
  [--account ACCOUNT] [--region REGION] [--kind KIND] [--q TEXT]
  list, changes and topology also accept --limit COUNT and --cursor CURSOR`

func runAssetsClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(assetsUsage)
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return usage
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
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "assets")
	if command != "list" {
		target = target.JoinPath(command)
	}
	target.RawQuery = query.Encode()
	return runProjectRequest(ctx, operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK,
		csv: export, responseLimit: 16 << 20}, token, nil, output, transport)
}
