package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const billingClientUsage = `usage: cloud-manager billing COMMAND TENANT PROJECT ACCOUNT PERIOD SOURCE
  get [REVISION]
  allocation-get [REPORT_REVISION [ALLOCATION_REVISION]]
  budget-get CURRENCY BASIS
  budget-create CURRENCY BASIS < budget.json
  budget-update CURRENCY BASIS REVISION < budget.json`

func runBillingClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if len(args) < 6 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return errors.New(billingClientUsage)
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "billing")
	query := url.Values{"account": {args[3]}, "period": {args[4]}, "source": {args[5]}}
	plan, err := planBillingRequest(target, query, args)
	if err != nil {
		return err
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
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
