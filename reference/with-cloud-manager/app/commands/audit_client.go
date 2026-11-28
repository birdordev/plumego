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
	"with-cloud-manager/app/handlers/governance"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/audit"
)

const auditUsage = "usage: cloud-manager audit {list,export} TENANT PROJECT [--category TYPE] [--account ACCOUNT] [--subject-id ID] [--from TIME] [--until TIME] (list adds --cursor and --limit)"

func RunAuditClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	usage := errors.New(auditUsage)
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) ||
		(args[0] != "list" && args[0] != "export") {
		return usage
	}
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	export := args[0] == "export"
	flags := flag.NewFlagSet("audit "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keys := []struct{ flag, query string }{
		{"category", "category"}, {"account", "account"}, {"subject-id", "subject_id"}, {"from", "from"}, {"until", "until"},
	}
	for _, key := range keys {
		flags.String(key.flag, "", "")
	}
	var limit int
	if !export {
		keys = append(keys, struct{ flag, query string }{"cursor", "cursor"})
		flags.String("cursor", "", "")
		flags.IntVar(&limit, "limit", audit.DefaultLimit, "")
	}
	if flags.Parse(args[3:]) != nil || flags.NArg() != 0 {
		return usage
	}
	query := make(url.Values)
	for _, key := range keys {
		if value := flags.Lookup(key.flag).Value.String(); value != "" {
			query.Set(key.query, value)
		}
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "audit")
	if export {
		target = target.JoinPath("export")
	} else {
		query.Set("limit", strconv.Itoa(limit))
	}
	target.RawQuery = query.Encode()
	if _, err := governance.ProjectAuditQuery(target.RawQuery, export); err != nil {
		return usage
	}
	return projecthttp.Run(ctx, projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK, CSV: export}, token, nil, output, transport)
}
