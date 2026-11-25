package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const catalogUsage = `usage: cloud-manager catalog COMMAND TENANT PROJECT [OPTIONS]
  list [--include-disabled] [--after ID] [--limit COUNT]
  get TEMPLATE
  version TEMPLATE VERSION
  create < template.json
  publish TEMPLATE --if-match REVISION < spec.json
  retire TEMPLATE --if-match REVISION
  launch TEMPLATE --idempotency-key KEY < launch.json`

func runCatalogClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return errors.New(catalogUsage)
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "service-catalog")
	plan, err := planCatalogRequest(target, args[0], args[3:])
	if err != nil {
		return err
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
}

func planCatalogRequest(target *url.URL, command string, args []string) (operationRequestPlan, error) {
	usage := errors.New(catalogUsage)
	if command == "list" {
		return planCatalogList(target, args)
	}
	if command == "create" && len(args) == 0 {
		return operationRequestPlan{target: target, method: http.MethodPost, status: http.StatusCreated, inputLimit: 4096}, nil
	}
	if len(args) < 1 || !operationClientSegment(args[0]) {
		return operationRequestPlan{}, usage
	}
	target = target.JoinPath(url.PathEscape(args[0]))
	switch command {
	case "get":
		if len(args) == 1 {
			return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
		}
	case "version":
		if len(args) == 2 {
			version, err := strconv.ParseInt(args[1], 10, 64)
			if err == nil && version > 0 && strconv.FormatInt(version, 10) == args[1] {
				return operationRequestPlan{target: target.JoinPath("versions", args[1]), method: http.MethodGet, status: http.StatusOK}, nil
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
			plan := operationRequestPlan{target: target, method: http.MethodDelete, status: http.StatusOK, match: strconv.Quote(strconv.FormatInt(*revision, 10))}
			if command == "publish" {
				plan.method, plan.inputLimit = http.MethodPut, 4096
			}
			return plan, nil
		}
	}
	return operationRequestPlan{}, usage
}

func planCatalogList(target *url.URL, args []string) (operationRequestPlan, error) {
	flags := flag.NewFlagSet("catalog list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	disabled := flags.Bool("include-disabled", false, "")
	after := flags.String("after", "", "")
	limit := flags.Int("limit", 50, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
		return operationRequestPlan{}, errors.New(catalogUsage)
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *disabled {
		query.Set("include_disabled", "true")
	}
	if *after != "" {
		query.Set("after", *after)
	}
	target.RawQuery = query.Encode()
	return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
}
