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

	"with-cloud-manager/internal/billing"
)

const billingClientUsage = `usage: cloud-manager billing COMMAND TENANT PROJECT ACCOUNT PERIOD SOURCE
  get [REVISION]
  allocation-get [REPORT_REVISION [ALLOCATION_REVISION]]
  allocation-preview REPORT_REVISION < policy.json
  allocation-publish REPORT_REVISION EXPECTED_ALLOCATION_REVISION REPORT_SHA256 POLICY_SHA256 < policy.json
  budget-get CURRENCY BASIS
  budget-create CURRENCY BASIS < budget.json
  budget-update CURRENCY BASIS REVISION < budget.json
  cloud-manager billing summary TENANT PROJECT FROM THROUGH SOURCE CURRENCY [--provider NAME] [--missing-only] [--limit N] [--after CURSOR]
  cloud-manager billing project-budget-get TENANT PROJECT PERIOD SOURCE CURRENCY BASIS
  cloud-manager billing project-budget-create TENANT PROJECT PERIOD SOURCE CURRENCY BASIS < budget.json
  cloud-manager billing project-budget-update TENANT PROJECT PERIOD SOURCE CURRENCY BASIS REVISION < budget.json`

func runBillingClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if len(args) < 6 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return errors.New(billingClientUsage)
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "billing")
	if args[0] == "summary" {
		plan, err := planBillingSummaryRequest(target, args)
		if err != nil {
			return err
		}
		return runProjectRequest(ctx, plan, token, nil, output, transport)
	}
	if strings.HasPrefix(args[0], "project-budget-") {
		plan, err := planProjectBudgetRequest(target, args)
		if err != nil {
			return err
		}
		return runProjectRequest(ctx, plan, token, input, output, transport)
	}
	query := url.Values{"account": {args[3]}, "period": {args[4]}, "source": {args[5]}}
	plan, err := planBillingRequest(target, query, args)
	if err != nil {
		return err
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
}

func planProjectBudgetRequest(target *url.URL, args []string) (operationRequestPlan, error) {
	usage := errors.New(billingClientUsage)
	expected := 7
	if args[0] == "project-budget-update" {
		expected++
	}
	if len(args) != expected {
		return operationRequestPlan{}, usage
	}
	query := url.Values{"period": {args[3]}, "source": {args[4]}, "currency": {args[5]}, "basis": {args[6]}}
	route := target.JoinPath("project-budget")
	route.RawQuery = query.Encode()
	if _, err := projectBudgetQuery(args[1], args[2], route.RawQuery); err != nil {
		return operationRequestPlan{}, usage
	}
	plan := operationRequestPlan{target: route, method: http.MethodGet, status: http.StatusOK}
	switch args[0] {
	case "project-budget-get":
	case "project-budget-create":
		plan.method, plan.status, plan.inputLimit = http.MethodPost, http.StatusCreated, 16<<10
	case "project-budget-update":
		if _, ok := billingRevision(args[7], false); !ok {
			return operationRequestPlan{}, usage
		}
		plan.method, plan.match, plan.inputLimit = http.MethodPut, strconv.Quote(args[7]), 16<<10
	default:
		return operationRequestPlan{}, usage
	}
	return plan, nil
}

func planBillingSummaryRequest(target *url.URL, args []string) (operationRequestPlan, error) {
	usage := errors.New(billingClientUsage)
	if len(args) < 7 {
		return operationRequestPlan{}, usage
	}
	flags := flag.NewFlagSet("billing summary", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	provider := flags.String("provider", "", "")
	missing := flags.Bool("missing-only", false, "")
	limit := flags.Int("limit", 50, "")
	after := flags.String("after", "", "")
	if err := flags.Parse(args[7:]); err != nil || flags.NArg() != 0 {
		return operationRequestPlan{}, usage
	}
	query := url.Values{
		"from": {args[3]}, "through": {args[4]}, "source": {args[5]}, "currency": {args[6]}, "limit": {strconv.Itoa(*limit)},
	}
	if *provider != "" {
		query.Set("provider", *provider)
	}
	if *missing {
		query.Set("missing_only", "true")
	}
	if *after != "" {
		query.Set("after", *after)
	}
	summary := target.JoinPath("summary")
	summary.RawQuery = query.Encode()
	if _, err := billingSummaryQuery(args[1], args[2], summary.RawQuery); err != nil {
		return operationRequestPlan{}, usage
	}
	return operationRequestPlan{target: summary, method: http.MethodGet, status: http.StatusOK}, nil
}

func planBillingRequest(target *url.URL, query url.Values, args []string) (operationRequestPlan, error) {
	usage := errors.New(billingClientUsage)
	plan := operationRequestPlan{method: http.MethodGet, status: http.StatusOK}
	switch args[0] {
	case "get":
		if len(args) == 7 {
			query.Set("revision", args[6])
		} else if len(args) != 6 {
			return operationRequestPlan{}, usage
		}
		if _, _, err := billingQuery(args[1], args[2], query.Encode()); err != nil {
			return operationRequestPlan{}, usage
		}
		plan.target = target.JoinPath("reports")
	case "allocation-get":
		if len(args) > 8 {
			return operationRequestPlan{}, usage
		}
		if len(args) >= 7 {
			query.Set("revision", args[6])
		}
		if len(args) == 8 {
			query.Set("allocation_revision", args[7])
		}
		if _, _, _, err := allocationQuery(args[1], args[2], query.Encode()); err != nil {
			return operationRequestPlan{}, usage
		}
		plan.target = target.JoinPath("allocations")
	case "allocation-preview", "allocation-publish":
		preview := args[0] == "allocation-preview"
		if (preview && len(args) != 7) || (!preview && len(args) != 10) {
			return operationRequestPlan{}, usage
		}
		query.Set("revision", args[6])
		plan.target = target.JoinPath("allocations")
		if preview {
			plan.target = plan.target.JoinPath("preview")
		} else {
			if _, valid := billingRevision(args[7], true); !valid {
				return operationRequestPlan{}, usage
			}
			plan.match = strconv.Quote(args[7])
			query.Set("report_sha256", args[8])
			query.Set("policy_sha256", args[9])
		}
		if _, err := allocationWriteQuery(args[1], args[2], query.Encode(), preview); err != nil {
			return operationRequestPlan{}, usage
		}
		plan.method, plan.inputLimit = http.MethodPost, billing.MaxAllocationPolicyBytes
	default:
		var err error
		plan, err = planBudgetRequest(target, query, args)
		if err != nil {
			return operationRequestPlan{}, err
		}
	}
	plan.target.RawQuery = query.Encode()
	return plan, nil
}

func planBudgetRequest(target *url.URL, query url.Values, args []string) (operationRequestPlan, error) {
	usage := errors.New(billingClientUsage)
	if len(args) != 8 && (len(args) != 9 || args[0] != "budget-update") {
		return operationRequestPlan{}, usage
	}
	query.Set("currency", args[6])
	query.Set("basis", args[7])
	if _, err := budgetQuery(args[1], args[2], query.Encode()); err != nil {
		return operationRequestPlan{}, usage
	}
	plan := operationRequestPlan{target: target.JoinPath("budget"), method: http.MethodGet, status: http.StatusOK}
	switch args[0] {
	case "budget-get":
	case "budget-create":
		plan.method, plan.status, plan.inputLimit = http.MethodPost, http.StatusCreated, 1024
	case "budget-update":
		if len(args) != 9 {
			return operationRequestPlan{}, usage
		}
		revision, err := strconv.ParseInt(args[8], 10, 64)
		if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != args[8] {
			return operationRequestPlan{}, usage
		}
		plan.method, plan.match, plan.inputLimit = http.MethodPut, strconv.Quote(args[8]), 1024
	default:
		return operationRequestPlan{}, usage
	}
	return plan, nil
}
