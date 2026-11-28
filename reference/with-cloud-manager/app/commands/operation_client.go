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
	"with-cloud-manager/internal/operations"
)

const operationsUsage = `usage: cloud-manager operations COMMAND TENANT PROJECT [OPTIONS]
  list [--status STATUS] [--queue-issue anomaly|overdue_recovery ...]
  backlog [--account ACCOUNT] [--region REGION]
  budget
  set-budget --if-match REVISION < limits.json
  latency --from TIME --until TIME [--account ACCOUNT] [--region REGION] [--action ACTION]
  verify-latency < acceptance.json
  get OPERATION
  events OPERATION [--after SEQUENCE] [--limit LIMIT]
  preview-delete --account ACCOUNT --region REGION --asset-id ID
  submit --idempotency-key KEY --if-match REVISION < request.json
  create-instance --idempotency-key KEY < request.json
  create-snapshot --idempotency-key KEY --if-match REVISION < request.json
  create-image --idempotency-key KEY --if-match REVISION < request.json
  copy-image --idempotency-key KEY --if-match REVISION < request.json
  delete-network --idempotency-key KEY --if-match REVISION < request.json
  retire-instance --idempotency-key KEY --if-match REVISION < request.json
  preview-retire --account ACCOUNT --region REGION --asset-id ID
  create-volume --idempotency-key KEY < request.json
  preview-volume-release --account ACCOUNT --region REGION --asset-id ID
  release-volume --idempotency-key KEY --if-match REVISION < request.json
  resize-instance-options --account ACCOUNT --region REGION --asset-id ID
  refresh-instance-resize-options --account ACCOUNT --region REGION --asset-id ID
  enable-bucket-encryption --idempotency-key KEY --if-match REVISION < request.json
  set-bucket-versioning --idempotency-key KEY --if-match REVISION < request.json
  resize-instance --idempotency-key KEY --if-match REVISION < request.json
  resize-volume --idempotency-key KEY --if-match REVISION < request.json
  rename-database-instance --idempotency-key KEY --if-match REVISION < request.json
  recover-database-instance --idempotency-key KEY --if-match REVISION < request.json
  register-deployment < deployment.json
  get-deployment DEPLOYMENT
  report-deployment DEPLOYMENT < receipt.json
  request-reclaim --idempotency-key KEY --if-match REVISION < request.json
  current-reclaim --account ACCOUNT --region SITE --system-id UUID
  get-reclaim RECLAIM
  review-reclaim RECLAIM < decision.json
  claim-reclaim RECLAIM < claim.json
  report-reclaim RECLAIM < receipt.json
  recover-reclaim RECLAIM < recovery.json
  review-reclaim-recovery RECLAIM < decision.json
  bare-metal-pxe --idempotency-key KEY --if-match REVISION < request.json
  bare-metal-power --idempotency-key KEY --if-match REVISION < request.json
  rename-cache-instance --idempotency-key KEY --if-match REVISION < request.json
  rename-load-balancer --idempotency-key KEY --if-match REVISION < request.json
  rename-load-balancer-listener --idempotency-key KEY --if-match REVISION < request.json
  rename-load-balancer-target-group --idempotency-key KEY --if-match REVISION < request.json
  set-load-balancer-backend-weight --idempotency-key KEY --if-match REVISION < request.json
  detach-load-balancer-backend --idempotency-key KEY --if-match REVISION < request.json
  create-dns-record --idempotency-key KEY --if-match REVISION < request.json
  delete-dns-record --idempotency-key KEY --if-match REVISION < request.json
  scale-workload --idempotency-key KEY --if-match REVISION < request.json
  update-dns-record --idempotency-key KEY --if-match REVISION < request.json
  set-dns-record-status --idempotency-key KEY --if-match REVISION < request.json
  attach-load-balancer-backend --idempotency-key KEY --if-match REVISION < request.json
  delete-image --idempotency-key KEY --if-match REVISION < request.json
  delete-snapshot --idempotency-key KEY --if-match REVISION < request.json
  rollback-snapshot --idempotency-key KEY --if-match REVISION < request.json
  restore-snapshot --idempotency-key KEY --if-match REVISION < request.json
  create-network --idempotency-key KEY [--if-match REVISION] < request.json
  revoke-security-rule --idempotency-key KEY --if-match REVISION < request.json
  release-elastic-ip --idempotency-key KEY --if-match REVISION < request.json
  bind-elastic-ip --idempotency-key KEY --if-match REVISION < request.json
  unbind-elastic-ip --idempotency-key KEY --if-match REVISION < request.json
  preview-batch < request.json
  submit-batch --idempotency-key KEY < request.json
  get-batch BATCH
  approve OPERATION --decision approve|reject [--deployment-id ID]
  resolve OPERATION --decision succeeded|failed --evidence-ref REF [--provider-request-id ID]
  retry-observation OPERATION --evidence-ref REF [--provider-resource-id RECORD_ID] [--repair-queue]`

func RunOperationsClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	if len(args) != 0 && args[0] == "verify-latency" {
		if len(args) != 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
			return errors.New(operationsUsage)
		}
		return runOperationLatencyGate(ctx, base, args[1], args[2], token, input, output, transport)
	}
	plan, err := PlanOperationRequest(base, args)
	if err != nil {
		return err
	}
	return projecthttp.Run(ctx, plan, token, input, output, transport)
}

func readOperationTarget(target *url.URL, args []string) (*url.URL, error) {
	usage := errors.New(operationsUsage)
	if args[0] == "list" {
		return operationListClientTarget(target, args[3:])
	}
	if len(args) < 4 || !models.ValidRequestSegment(args[3]) {
		return nil, usage
	}
	target = target.JoinPath(url.PathEscape(args[3]))
	switch {
	case args[0] == "get" && len(args) == 4:
		return target, nil
	case args[0] == "events":
		flags := flag.NewFlagSet("operations events", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		after := flags.Int64("after", 0, "")
		limit := flags.Int("limit", 50, "")
		if flags.Parse(args[4:]) != nil || flags.NArg() != 0 || *after < 0 || *limit < 1 || *limit > 100 {
			return nil, usage
		}
		target = target.JoinPath("events")
		target.RawQuery = url.Values{"after": {strconv.FormatInt(*after, 10)}, "limit": {strconv.Itoa(*limit)}}.Encode()
		return target, nil
	}
	return nil, usage
}

func operationListClientTarget(target *url.URL, args []string) (*url.URL, error) {
	flags := flag.NewFlagSet("operations list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keys := []string{"account", "region", "kind", "action", "status", "cursor"}
	for _, key := range keys {
		flags.String(key, "", "")
	}
	queueIssue := flags.String("queue-issue", "", "")
	limit := flags.Int("limit", operations.DefaultOperationPageSize, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
		return nil, errors.New(operationsUsage)
	}
	values := url.Values{"limit": {strconv.Itoa(*limit)}}
	switch *queueIssue {
	case "":
	case "anomaly", "overdue_recovery":
		values.Set("queue_issue", *queueIssue)
	default:
		return nil, errors.New(operationsUsage)
	}
	for _, key := range keys {
		if value := flags.Lookup(key).Value.String(); value != "" {
			values.Set(key, value)
		}
	}
	target.RawQuery = values.Encode()
	return target, nil
}
