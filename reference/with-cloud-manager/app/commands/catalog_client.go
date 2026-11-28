package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/models"
)

const catalogUsage = `usage: cloud-manager catalog COMMAND TENANT PROJECT [OPTIONS]
  list [--include-disabled] [--after ID] [--limit COUNT]
  get TEMPLATE
  version TEMPLATE VERSION
  create < template.json
  publish TEMPLATE --if-match REVISION < spec.json
  retire TEMPLATE --if-match REVISION
  launch TEMPLATE --idempotency-key KEY < launch.json`

func RunCatalogClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return errors.New(catalogUsage)
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "service-catalog")
	plan, err := planCatalogRequest(target, args[0], args[3:])
	if err != nil {
		return err
	}
	return projecthttp.Run(ctx, plan, token, input, output, transport)
}

func planCatalogRequest(target *url.URL, command string, args []string) (projecthttp.Request, error) {
	usage := errors.New(catalogUsage)
	if command == "list" {
		return planCatalogList(target, args)
	}
	if command == "create" && len(args) == 0 {
		return projecthttp.Request{Target: target, Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 4096}, nil
	}
	if len(args) < 1 || !models.ValidRequestSegment(args[0]) {
		return projecthttp.Request{}, usage
	}
	target = target.JoinPath(url.PathEscape(args[0]))
	switch command {
	case "get":
		if len(args) == 1 {
			return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	case "version":
		if len(args) == 2 {
			version, err := strconv.ParseInt(args[1], 10, 64)
			if err == nil && version > 0 && strconv.FormatInt(version, 10) == args[1] {
				return projecthttp.Request{Target: target.JoinPath("versions", args[1]), Method: http.MethodGet, Status: http.StatusOK}, nil
			}
		}
	case "launch":
		plan, err := planOperationSubmission(target.JoinPath("launch"), command, args[1:])
		if err == nil {
			return plan, nil
		}
	case "publish", "retire":
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		revision := flags.Int64("if-match", 0, "")
		if flags.Parse(args[1:]) == nil && flags.NArg() == 0 && *revision > 0 {
			plan := projecthttp.Request{Target: target, Method: http.MethodDelete, Status: http.StatusOK, Match: strconv.Quote(strconv.FormatInt(*revision, 10))}
			if command == "publish" {
				plan.Method, plan.InputLimit = http.MethodPut, 4096
			}
			return plan, nil
		}
	}
	return projecthttp.Request{}, usage
}

func planCatalogList(target *url.URL, args []string) (projecthttp.Request, error) {
	flags := flag.NewFlagSet("catalog list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	disabled := flags.Bool("include-disabled", false, "")
	after := flags.String("after", "", "")
	limit := flags.Int("limit", 50, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
		return projecthttp.Request{}, errors.New(catalogUsage)
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *disabled {
		query.Set("include_disabled", "true")
	}
	if *after != "" {
		query.Set("after", *after)
	}
	target.RawQuery = query.Encode()
	return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
}
