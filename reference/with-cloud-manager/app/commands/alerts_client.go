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
	"with-cloud-manager/internal/alerts"
)

const alertsUsage = `usage: cloud-manager alerts COMMAND TENANT PROJECT [OPTIONS]
  list [--status active|open|acknowledged|resolved|all] [--rule RULE] [--after ID] [--limit COUNT]
  get ALERT_ID
  acknowledge ALERT_ID --if-match REVISION`

func RunAlertsClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return errors.New(alertsUsage)
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "alerts")
	plan, err := planAlertRequest(target, args[0], args[3:])
	if err != nil {
		return err
	}
	return projecthttp.Run(ctx, plan, token, nil, output, transport)
}

func planAlertRequest(target *url.URL, command string, args []string) (projecthttp.Request, error) {
	usage := errors.New(alertsUsage)
	plan := projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}
	flags := flag.NewFlagSet("alerts "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if command == "list" {
		status := flags.String("status", "active", "")
		rule := flags.String("rule", "", "")
		after := flags.String("after", "", "")
		limit := flags.Int("limit", alerts.DefaultLimit, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
			return projecthttp.Request{}, usage
		}
		target.RawQuery = url.Values{"status": {*status}, "rule": {*rule}, "after": {*after}, "limit": {strconv.Itoa(*limit)}}.Encode()
		return plan, nil
	}
	if len(args) < 1 || !models.ValidRequestSegment(args[0]) {
		return projecthttp.Request{}, usage
	}
	plan.Target = target.JoinPath(url.PathEscape(args[0]))
	switch command {
	case "get":
		if len(args) == 1 {
			return plan, nil
		}
	case "acknowledge":
		revision := flags.Int64("if-match", 0, "")
		if flags.Parse(args[1:]) == nil && flags.NArg() == 0 && *revision > 0 {
			plan.Target = plan.Target.JoinPath("acknowledgement")
			plan.Method = http.MethodPost
			plan.Match = strconv.Quote(strconv.FormatInt(*revision, 10))
			return plan, nil
		}
	}
	return projecthttp.Request{}, usage
}
