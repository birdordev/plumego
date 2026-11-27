package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"with-cloud-manager/internal/operations"
)

const operationsUsage = `usage: cloud-manager operations COMMAND TENANT PROJECT [OPTIONS]
  list [--status STATUS] [--queue-issue anomaly|overdue_recovery ...]
  backlog [--account ACCOUNT] [--region REGION]
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
  resize-volume --idempotency-key KEY --if-match REVISION < request.json
  rename-database-instance --idempotency-key KEY --if-match REVISION < request.json
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

func runOperationsClient(ctx context.Context, args []string, address, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	if len(args) != 0 && args[0] == "verify-latency" {
		if len(args) != 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
			return errors.New(operationsUsage)
		}
		return runOperationLatencyGate(ctx, base, args[1], args[2], token, input, output, transport)
	}
	plan, err := planOperationRequest(base, args)
	if err != nil {
		return err
	}
	return runProjectRequest(ctx, plan, token, input, output, transport)
}

func runProjectRequest(ctx context.Context, plan operationRequestPlan, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if !validProjectClientToken(token) {
		return errors.New("a valid CLOUD_MANAGER_API_TOKEN is required")
	}
	body := plan.body
	if plan.inputLimit > 0 {
		var err error
		body, err = readOperationInput(input, plan.inputLimit)
		if err != nil {
			return err
		}
	}
	timeout := plan.timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, plan.method, plan.target.String(), reader)
	if err != nil {
		return errors.New("invalid operations request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	expectedMedia, responseLimit := "application/json", 1<<20
	if plan.csv {
		expectedMedia, responseLimit = "text/csv", 16<<20
	}
	if plan.responseLimit > 0 {
		responseLimit = plan.responseLimit
	}
	request.Header.Set("Accept", expectedMedia)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if plan.method != http.MethodGet {
		request.GetBody = nil
	}
	if plan.key != "" {
		request.Header.Set("Idempotency-Key", plan.key)
	}
	if plan.match != "" {
		request.Header.Set("If-Match", plan.match)
	}
	client := projectAPIClient(transport)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("operations API request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != plan.status {
		return fmt.Errorf("operations API returned HTTP %d", response.StatusCode)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != expectedMedia {
		return errors.New("invalid operations API response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil || len(data) > responseLimit {
		return errors.New("cannot read operations API response")
	}
	if plan.csv {
		_, err := output.Write(data)
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Data) == 0 || envelope.Data[0] != '{' {
		return errors.New("invalid operations API response")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Data); err != nil {
		return errors.New("invalid operations API response")
	}
	_, err = fmt.Fprintln(output, compact.String())
	return err
}

func projectAPIClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func validProjectClientToken(token string) bool {
	return token != "" && len(token) <= 4096 &&
		strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func operationClientBase(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, errors.New("CLOUD_MANAGER_API_URL must be an HTTPS API base URL or a loopback HTTP URL")
	}
	if u.Scheme == "https" {
		return u, nil
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if u.Scheme == "http" && err == nil && ip.Unmap().IsLoopback() {
		return u, nil
	}
	return nil, errors.New("CLOUD_MANAGER_API_URL must be an HTTPS API base URL or a loopback HTTP URL")
}

func readOperationTarget(target *url.URL, args []string) (*url.URL, error) {
	usage := errors.New(operationsUsage)
	if args[0] == "list" {
		return operationListClientTarget(target, args[3:])
	}
	if len(args) < 4 || !operationClientSegment(args[3]) {
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

func operationClientSegment(segment string) bool {
	return segment != "" && len(segment) <= 256 && segment != "." && segment != ".." &&
		!strings.ContainsAny(segment, "/\\") && strings.IndexFunc(segment, unicode.IsControl) < 0
}
