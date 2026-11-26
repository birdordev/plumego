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
  idle [--state STATE] [--account ACCOUNT] [--region REGION] [--cursor CURSOR] [--limit LIMIT]`

func runGovernanceClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(governanceUsage)
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return usage
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "governance")
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
	default:
		return usage
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
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
