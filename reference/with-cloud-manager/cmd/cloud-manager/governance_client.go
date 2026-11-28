package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const governanceUsage = `usage: cloud-manager governance COMMAND TENANT PROJECT
  policy-get [REVISION]
  policy-create < policy.json
  policy-update REVISION < policy.json
  ticket-policy-get [REVISION]
  ticket-policy-create < policy.json
  ticket-policy-update REVISION < policy.json
  ticket-get OPERATION
  ticket-record OPERATION < evidence.json
  compliance KIND [--state STATE] [--account ACCOUNT] [--region REGION] [--cursor CURSOR] [--limit LIMIT]
  idle [--state STATE] [--account ACCOUNT] [--region REGION] [--cursor CURSOR] [--limit LIMIT]
  inventory-freshness [--provider PROVIDER] [--account ACCOUNT] [--state STATE] [--dispatch-state STATE] [--order next_at] [--window-seconds SECONDS] [--after CURSOR] [--limit LIMIT]
  database-protection [--provider PROVIDER] [--account ACCOUNT] [--region REGION] [--finding FINDING] [--window-seconds SECONDS] [--max-age-seconds SECONDS] [--after CURSOR] [--limit COUNT]
  bucket-protection [--provider PROVIDER] [--account ACCOUNT] [--location REGION] [--finding FINDING] [--window-seconds SECONDS] [--after CURSOR] [--limit LIMIT]
  inventory-sample {--expected-scopes COUNT|--scope-manifest < scopes.json} [--provider PROVIDER] [--account ACCOUNT] [--samples COUNT] [--interval DURATION]
  asset-capacity < workload.json
` + dispatchStatsUsage

func runGovernanceClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if len(args) > 0 && args[0] == "inventory-dispatch-stats" {
		return runDispatchStats(ctx, args[1:], input, output)
	}
	usage := errors.New(governanceUsage)
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return usage
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	project := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]))
	target := project.JoinPath("governance")
	plan := operationRequestPlan{target: target.JoinPath("tag-policy"), method: http.MethodGet, status: http.StatusOK}
	switch args[0] {
	case "policy-get", "ticket-policy-get":
		if args[0] == "ticket-policy-get" {
			plan.target = target.JoinPath("ticket-policy")
		}
		if len(args) == 4 && validTagRevision(args[3]) {
			plan.target.RawQuery = url.Values{"revision": {args[3]}}.Encode()
		} else if len(args) != 3 {
			return usage
		}
	case "policy-create", "ticket-policy-create":
		if len(args) != 3 {
			return usage
		}
		plan.method, plan.status, plan.inputLimit = http.MethodPost, http.StatusCreated, tagPolicyInputLimit
		if args[0] == "ticket-policy-create" {
			plan.target, plan.inputLimit = target.JoinPath("ticket-policy"), ticketPolicyInputLimit
		}
	case "policy-update", "ticket-policy-update":
		if len(args) != 4 || !validTagRevision(args[3]) {
			return usage
		}
		plan.method, plan.match, plan.inputLimit = http.MethodPut, strconv.Quote(args[3]), tagPolicyInputLimit
		if args[0] == "ticket-policy-update" {
			plan.target, plan.inputLimit = target.JoinPath("ticket-policy"), ticketPolicyInputLimit
		}
	case "ticket-get", "ticket-record":
		if len(args) != 4 || !operationClientSegment(args[3]) {
			return usage
		}
		plan.target = target.JoinPath("change-tickets", url.PathEscape(args[3]))
		if args[0] == "ticket-record" {
			plan.method, plan.inputLimit = http.MethodPost, ticketEvidenceInputLimit
		}
	case "compliance":
		if len(args) < 4 || !provider.Kind(args[3]).Valid() {
			return usage
		}
		query, ok := governanceReportOptions(args[4:], "compliant", "noncompliant", "unknown")
		if !ok {
			return usage
		}
		query.Set("kind", args[3])
		plan.target = target.JoinPath("tag-compliance")
		plan.target.RawQuery = query.Encode()
	case "idle":
		query, ok := governanceReportOptions(args[3:], "candidate", "active", "insufficient", "not_running")
		if !ok {
			return usage
		}
		plan.target = target.JoinPath("idle-instances")
		plan.target.RawQuery = query.Encode()
	case "inventory-freshness":
		query, ok := inventoryFreshnessOptions(args[3:])
		if !ok {
			return usage
		}
		plan.target = target.JoinPath("inventory-freshness")
		plan.target.RawQuery = query.Encode()
	case "bucket-protection":
		query, ok := bucketProtectionOptions(args[3:])
		if !ok {
			return usage
		}
		plan.target = target.JoinPath("bucket-protection")
		plan.target.RawQuery = query.Encode()
	case "database-protection":
		query, ok := databaseProtectionOptions(args[3:])
		if !ok {
			return usage
		}
		plan.target = target.JoinPath("database-protection")
		plan.target.RawQuery = query.Encode()
	case "inventory-sample":
		options, ok := inventorySampleOptions(args[3:])
		if !ok {
			return usage
		}
		scope := inventorySampleScope{
			Tenant: args[1], Project: args[2], Provider: options.query.Get("provider"), Account: options.query.Get("account"),
		}
		if options.manifest {
			if err := options.readManifest(input, scope); err != nil {
				return err
			}
		}
		plan.target = target.JoinPath("inventory-freshness")
		plan.target.RawQuery = options.query.Encode()
		return runInventorySamples(ctx, plan, token, output, transport, options, scope)
	case "asset-capacity":
		if len(args) != 3 {
			return usage
		}
		return runAssetCapacityClient(ctx, project, args[1], args[2], token, input, output, transport)
	default:
		return usage
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
}

func bucketProtectionOptions(args []string) (url.Values, bool) {
	flags := flag.NewFlagSet("bucket-protection", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cloud := flags.String("provider", "", "")
	account := flags.String("account", "", "")
	location := flags.String("location", "", "")
	finding := flags.String("finding", "", "")
	window := flags.Int("window-seconds", inventory.DefaultBucketProtectionWindow, "")
	after := flags.String("after", "", "")
	limit := flags.Int("limit", inventory.DefaultBucketProtectionLimit, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 || *window < 60 || *window > 604800 {
		return nil, false
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "window_seconds": {strconv.Itoa(*window)}}
	for key, value := range map[string]string{
		"provider": *cloud, "account": *account, "location": *location, "finding": *finding, "after": *after,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query, true
}

func inventoryFreshnessOptions(args []string) (url.Values, bool) {
	flags := flag.NewFlagSet("inventory-freshness", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cloud := flags.String("provider", "", "")
	account := flags.String("account", "", "")
	state := flags.String("state", "", "")
	dispatch := flags.String("dispatch-state", "", "")
	order := flags.String("order", "", "")
	window := flags.Int("window-seconds", inventory.DefaultFreshnessWindow, "")
	after := flags.String("after", "", "")
	limit := flags.Int("limit", inventory.DefaultFreshnessLimit, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 || *window < 60 || *window > 604800 {
		return nil, false
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "window_seconds": {strconv.Itoa(*window)}}
	for key, value := range map[string]string{
		"provider": *cloud, "account": *account, "state": *state, "dispatch_state": *dispatch, "order": *order, "after": *after,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query, true
}

func validTagRevision(value string) bool {
	revision, err := strconv.ParseInt(value, 10, 64)
	return err == nil && revision > 0 && strconv.FormatInt(revision, 10) == value
}

func governanceReportOptions(args []string, states ...string) (url.Values, bool) {
	flags := flag.NewFlagSet("governance-report", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state", "", "")
	account := flags.String("account", "", "")
	region := flags.String("region", "", "")
	cursor := flags.String("cursor", "", "")
	limit := flags.Int("limit", 50, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *limit < 1 || *limit > 200 ||
		(*state != "" && !slices.Contains(states, *state)) {
		return nil, false
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	for key, value := range map[string]string{"state": *state, "account": *account, "region": *region, "cursor": *cursor} {
		if value != "" {
			query.Set(key, value)
		}
	}
	return query, true
}
