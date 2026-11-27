package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

type operationRequestPlan struct {
	target        *url.URL
	method        string
	status        int
	key           string
	match         string
	body          []byte
	inputLimit    int
	responseLimit int
	csv           bool
	timeout       time.Duration
}

func planOperationRequest(base *url.URL, args []string) (operationRequestPlan, error) {
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	project := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]))
	ledger := project.JoinPath("operations")
	switch args[0] {
	case "backlog":
		if query, ok := backlogClientQuery(args[3:]); ok {
			target := project.JoinPath("operation-backlog")
			target.RawQuery = query.Encode()
			return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
		}
	case "latency":
		if query, ok := latencyClientQuery(args[3:]); ok {
			target := project.JoinPath("operation-latency")
			target.RawQuery = query.Encode()
			return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
		}
	case "list", "get", "events":
		target, err := readOperationTarget(ledger, args)
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, err
	case "preview-retire":
		return planOperationAssetRead(project.JoinPath("instance-operations", "retirement-preview"), "asset_id", args[3:])
	case "retire-instance":
		return planOperationSubmission(project.JoinPath("instance-operations", "retire"), args[0], args[3:])
	case "preview-delete":
		return planOperationAssetRead(project.JoinPath("instance-operations", "deletion-preview"), "asset_id", args[3:])
	case "submit":
		return planOperationSubmission(project.JoinPath("instance-operations"), args[0], args[3:])
	case "create-instance":
		return planOperationSubmission(project.JoinPath("instances"), args[0], args[3:])
	case "create-snapshot":
		return planOperationSubmission(project.JoinPath("snapshots"), args[0], args[3:])
	case "create-image":
		return planOperationSubmission(project.JoinPath("images"), args[0], args[3:])
	case "copy-image":
		return planOperationSubmission(project.JoinPath("images", "copy"), args[0], args[3:])
	case "delete-image":
		return planOperationSubmission(project.JoinPath("images", "delete"), args[0], args[3:])
	case "delete-snapshot":
		return planOperationSubmission(project.JoinPath("snapshots", "delete"), args[0], args[3:])
	case "rollback-snapshot":
		return planOperationSubmission(project.JoinPath("snapshots", "rollback"), args[0], args[3:])
	case "restore-snapshot":
		return planOperationSubmission(project.JoinPath("snapshots", "restore"), args[0], args[3:])
	case "delete-network":
		return planOperationSubmission(project.JoinPath("network-operations", "delete"), args[0], args[3:])
	case "create-dns-record":
		return planOperationSubmission(project.JoinPath("dns-zones", "records"), args[0], args[3:])
	case "delete-dns-record":
		return planOperationSubmission(project.JoinPath("dns-zones", "record-deletion"), args[0], args[3:])
	case "scale-workload":
		return planOperationSubmission(project.JoinPath("kubernetes-workloads", "scale"), args[0], args[3:])
	case "set-dns-record-status":
		return planOperationSubmission(project.JoinPath("dns-zones", "record-status"), args[0], args[3:])
	case "attach-load-balancer-backend":
		return planOperationSubmission(project.JoinPath("load-balancers", "attach-backend"), args[0], args[3:])
	case "detach-load-balancer-backend":
		return planOperationSubmission(project.JoinPath("load-balancers", "detach-backend"), args[0], args[3:])
	case "set-load-balancer-backend-weight":
		return planOperationSubmission(project.JoinPath("load-balancers", "backend-weight"), args[0], args[3:])
	case "rename-load-balancer-target-group":
		return planOperationSubmission(project.JoinPath("load-balancers", "rename-target-group"), args[0], args[3:])
	case "rename-load-balancer-listener":
		return planOperationSubmission(project.JoinPath("load-balancers", "rename-listener"), args[0], args[3:])
	case "register-deployment":
		if len(args) == 3 {
			return operationRequestPlan{target: project.JoinPath("bare-metal", "deployments"),
				method: http.MethodPost, status: http.StatusCreated, inputLimit: 2048}, nil
		}
	case "get-deployment", "report-deployment":
		if len(args) == 4 && operationClientSegment(args[3]) {
			target := project.JoinPath("bare-metal", "deployments", url.PathEscape(args[3]))
			if args[0] == "get-deployment" {
				return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
			}
			return operationRequestPlan{target: target.JoinPath("receipt"), method: http.MethodPost,
				status: http.StatusOK, inputLimit: 2048}, nil
		}
	case "bare-metal-pxe":
		return planOperationSubmission(project.JoinPath("bare-metal", "pxe"), args[0], args[3:])
	case "bare-metal-power":
		return planOperationSubmission(project.JoinPath("bare-metal", "power"), args[0], args[3:])
	case "request-reclaim":
		return planOperationSubmission(project.JoinPath("bare-metal", "reclaims"), args[0], args[3:])
	case "current-reclaim":
		return planOperationAssetRead(project.JoinPath("bare-metal", "reclaims", "current"), "system_id", args[3:])
	case "get-reclaim", "review-reclaim", "claim-reclaim", "report-reclaim", "recover-reclaim", "review-reclaim-recovery":
		return planBareMetalReclaim(project.JoinPath("bare-metal", "reclaims"), args[0], args[3:])
	case "rename-cache-instance":
		return planOperationSubmission(project.JoinPath("caches", "rename"), args[0], args[3:])
	case "rename-database-instance":
		return planOperationSubmission(project.JoinPath("databases", "rename"), args[0], args[3:])
	case "rename-load-balancer":
		return planOperationSubmission(project.JoinPath("load-balancers", "rename"), args[0], args[3:])
	case "create-network":
		return planOperationSubmission(project.JoinPath("network-operations"), args[0], args[3:])
	case "revoke-security-rule":
		return planOperationSubmission(project.JoinPath("network-operations", "revoke-rule"), args[0], args[3:])
	case "release-elastic-ip", "bind-elastic-ip", "unbind-elastic-ip":
		return planOperationSubmission(project.JoinPath("network-operations", args[0]), args[0], args[3:])
	case "preview-batch":
		if len(args) == 3 {
			return operationRequestPlan{target: project.JoinPath("operation-batches", "preview"), method: http.MethodPost,
				status: http.StatusOK, inputLimit: batchBodyLimit}, nil
		}
	case "submit-batch":
		return planOperationSubmission(project.JoinPath("operation-batches"), args[0], args[3:])
	case "get-batch":
		if len(args) == 4 && operationClientSegment(args[3]) {
			return operationRequestPlan{target: project.JoinPath("operation-batches", url.PathEscape(args[3])),
				method: http.MethodGet, status: http.StatusOK}, nil
		}
	case "approve", "resolve", "retry-observation":
		if len(args) < 4 || !operationClientSegment(args[3]) {
			return operationRequestPlan{}, errors.New(operationsUsage)
		}
		return planOperationDecision(ledger.JoinPath(url.PathEscape(args[3])), args[0], args[4:])
	}
	return operationRequestPlan{}, errors.New(operationsUsage)
}

func planBareMetalReclaim(base *url.URL, command string, args []string) (operationRequestPlan, error) {
	if len(args) != 1 || !operationClientSegment(args[0]) {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	target := base.JoinPath(url.PathEscape(args[0]))
	if command == "get-reclaim" {
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
	}
	var suffix string
	switch command {
	case "review-reclaim":
		suffix = "approval"
	case "claim-reclaim":
		suffix = "claim"
	case "report-reclaim":
		suffix = "receipt"
	case "recover-reclaim":
		suffix = "recovery"
	case "review-reclaim-recovery":
		suffix = "recovery/approval"
	}
	return operationRequestPlan{target: target.JoinPath(suffix), method: http.MethodPost, status: http.StatusOK, inputLimit: 2048}, nil
}

func planOperationAssetRead(target *url.URL, idField string, args []string) (operationRequestPlan, error) {
	flags := flag.NewFlagSet("operations asset query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account := flags.String("account", "", "")
	region := flags.String("region", "", "")
	asset := flags.String(strings.ReplaceAll(idField, "_", "-"), "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *account == "" || *region == "" || *asset == "" {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	target.RawQuery = url.Values{"account": {*account}, "region": {*region}, idField: {*asset}}.Encode()
	return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
}

func planOperationSubmission(target *url.URL, command string, args []string) (operationRequestPlan, error) {
	flags := flag.NewFlagSet("operations "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	key := flags.String("idempotency-key", "", "")
	var match string
	requireMatch := command == "request-reclaim" || command == "bare-metal-pxe" || command == "bare-metal-power" || command == "retire-instance" || command == "rename-database-instance" || command == "rename-cache-instance" || command == "scale-workload" || command == "create-dns-record" || command == "delete-dns-record" || command == "set-dns-record-status" || command == "submit" || command == "delete-network" || command == "rename-load-balancer" || command == "rename-load-balancer-listener" || command == "rename-load-balancer-target-group" || command == "set-load-balancer-backend-weight" || command == "detach-load-balancer-backend" || command == "attach-load-balancer-backend" || command == "revoke-security-rule" || command == "release-elastic-ip" ||
		command == "bind-elastic-ip" || command == "unbind-elastic-ip" || command == "create-snapshot" || command == "delete-snapshot" || command == "delete-image" || command == "create-image" || command == "copy-image" || command == "restore-snapshot" || command == "rollback-snapshot"
	if requireMatch || command == "create-network" {
		flags.Func("if-match", "", func(value string) error {
			revision, err := strconv.ParseInt(value, 10, 64)
			if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != value {
				return errors.New("invalid resource revision")
			}
			match = strconv.Quote(value)
			return nil
		})
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 || *key == "" || len(*key) > 128 ||
		strings.IndexFunc(*key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 ||
		(requireMatch && match == "") {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	bodyLimit := 4096
	switch command {
	case "submit-batch":
		bodyLimit = batchBodyLimit
	case "request-reclaim":
		bodyLimit = 2048
	}
	return operationRequestPlan{target: target, method: http.MethodPost, status: http.StatusAccepted,
		key: *key, match: match, inputLimit: bodyLimit}, nil
}

func planOperationDecision(target *url.URL, command string, args []string) (operationRequestPlan, error) {
	usage := errors.New(operationsUsage)
	flags := flag.NewFlagSet("operations "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var body any
	var path string
	switch command {
	case "approve":
		var input operations.Approval
		flags.StringVar(&input.Decision, "decision", "", "")
		flags.StringVar(&input.DeploymentID, "deployment-id", "", "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || (input.Decision != "approve" && input.Decision != "reject") ||
			(input.DeploymentID != "" && input.Decision != "approve") {
			return operationRequestPlan{}, usage
		}
		body, path = input, "approval"
	case "resolve":
		var input operations.Resolution
		flags.StringVar(&input.Decision, "decision", "", "")
		flags.StringVar(&input.EvidenceRef, "evidence-ref", "", "")
		flags.StringVar(&input.ProviderRequestID, "provider-request-id", "", "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || input.EvidenceRef == "" ||
			(input.Decision != "succeeded" && input.Decision != "failed") {
			return operationRequestPlan{}, usage
		}
		body, path = input, "resolution"
	case "retry-observation":
		var input operations.ObservationRetry
		flags.StringVar(&input.EvidenceRef, "evidence-ref", "", "")
		flags.StringVar(&input.ProviderResourceID, "provider-resource-id", "", "")
		flags.BoolVar(&input.RepairQueue, "repair-queue", false, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || input.EvidenceRef == "" ||
			(input.ProviderResourceID != "" && !provider.ValidDNSRecordID(input.ProviderResourceID)) {
			return operationRequestPlan{}, usage
		}
		body, path = input, "retry-observation"
	default:
		return operationRequestPlan{}, usage
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return operationRequestPlan{}, err
	}
	return operationRequestPlan{target: target.JoinPath(path), method: http.MethodPost, status: http.StatusOK, body: payload}, nil
}

func readOperationInput(input io.Reader, limit int) ([]byte, error) {
	if input == nil {
		return nil, errors.New("a JSON request on standard input is required")
	}
	raw, err := io.ReadAll(io.LimitReader(input, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, fmt.Errorf("could not read a request of at most %d bytes", limit)
	}
	body := bytes.TrimSpace(raw)
	if len(body) == 0 || body[0] != '{' || !json.Valid(body) {
		return nil, errors.New("standard input must contain one JSON object")
	}
	return body, nil
}
