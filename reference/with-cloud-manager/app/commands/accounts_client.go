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
	"with-cloud-manager/app/handlers/accounts"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
)

const cloudAccountsUsage = `usage: cloud-manager cloud-accounts COMMAND TENANT PROJECT
  list [--after ID] [--limit COUNT]
  get ACCOUNT
  grants ACCOUNT [--after PROJECT] [--limit COUNT]
  register < account.json
  change ACCOUNT REVISION < change.json
  rollout ACCOUNT
  rollout-replace ACCOUNT REVISION < rollout.json
  schedules ACCOUNT [--after ID] [--limit COUNT]
  schedule-add ACCOUNT < schedule.json
  schedule-change ACCOUNT ID REVISION < change.json`

func RunCloudAccountsClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(cloudAccountsUsage)
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return usage
	}
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	plan := projecthttp.Request{Target: base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "cloud-accounts"),
		Method: http.MethodGet, Status: http.StatusOK}
	options := args[3:]
	switch args[0] {
	case "rollout", "rollout-replace":
		if !planAccountRollout(&plan, args[0], options) {
			return usage
		}
	case "schedules", "schedule-add", "schedule-change":
		if !planAccountSchedule(&plan, args[0], options) {
			return usage
		}
	case "register":
		if len(options) != 0 {
			return usage
		}
		plan.Method, plan.Status, plan.InputLimit = http.MethodPost, http.StatusCreated, accounts.AccountInputLimit
	case "get", "grants", "change":
		if len(options) == 0 || !models.ValidRequestSegment(options[0]) {
			return usage
		}
		plan.Target = plan.Target.JoinPath(url.PathEscape(options[0]))
		switch args[0] {
		case "get":
			if len(options) != 1 {
				return usage
			}
		case "grants":
			query, ok := cloudAccountPageOptions(options[1:])
			if !ok {
				return usage
			}
			plan.Target = plan.Target.JoinPath("grants")
			plan.Target.RawQuery = query.Encode()
		case "change":
			if len(options) != 2 {
				return usage
			}
			revision, err := strconv.ParseInt(options[1], 10, 64)
			if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != options[1] {
				return usage
			}
			plan.Method, plan.Match, plan.InputLimit = http.MethodPatch, strconv.Quote(options[1]), accounts.AccountInputLimit
		}
	case "list":
		query, ok := cloudAccountPageOptions(options)
		if !ok {
			return usage
		}
		plan.Target.RawQuery = query.Encode()
	default:
		return usage
	}
	return projecthttp.Run(ctx, plan, token, input, output, transport)
}

func planAccountRollout(plan *projecthttp.Request, command string, args []string) bool {
	if len(args) == 0 || !models.ValidRequestSegment(args[0]) {
		return false
	}
	plan.Target = plan.Target.JoinPath(url.PathEscape(args[0]), "operation-rollout")
	if command == "rollout" {
		return len(args) == 1
	}
	if len(args) != 2 {
		return false
	}
	revision, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || revision < 0 || strconv.FormatInt(revision, 10) != args[1] {
		return false
	}
	plan.Method, plan.Match, plan.InputLimit = http.MethodPut, strconv.Quote(args[1]), accounts.AccountRolloutInputLimit
	return true
}

func planAccountSchedule(plan *projecthttp.Request, command string, args []string) bool {
	if len(args) < 1 || !models.ValidRequestSegment(args[0]) {
		return false
	}
	plan.Target = plan.Target.JoinPath(url.PathEscape(args[0]), "inventory-schedules")
	switch command {
	case "schedules":
		query, ok := cloudAccountPageOptions(args[1:])
		if !ok {
			return false
		}
		plan.Target.RawQuery = query.Encode()
	case "schedule-add":
		if len(args) != 1 {
			return false
		}
		plan.Method, plan.Status, plan.InputLimit = http.MethodPost, http.StatusCreated, accounts.AccountInputLimit
	case "schedule-change":
		if len(args) != 3 {
			return false
		}
		if _, err := controller.ParseScheduleID(args[1]); err != nil {
			return false
		}
		if _, err := controller.ParseScheduleID(args[2]); err != nil {
			return false
		}
		plan.Target = plan.Target.JoinPath(args[1])
		plan.Method, plan.Match, plan.InputLimit = http.MethodPatch, strconv.Quote(args[2]), accounts.AccountInputLimit
	}
	return true
}

func cloudAccountPageOptions(args []string) (url.Values, bool) {
	flags := flag.NewFlagSet("cloud-accounts", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	after := flags.String("after", "", "")
	limit := flags.Int("limit", identity.DefaultAccountPageLimit, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
		return nil, false
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *after != "" {
		query.Set("after", *after)
	}
	return query, true
}
