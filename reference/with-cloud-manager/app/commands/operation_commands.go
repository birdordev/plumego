package commands

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"with-cloud-manager/app/clients/projecthttp"
	operationhttp "with-cloud-manager/app/handlers/operations"
	"with-cloud-manager/app/models"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func PlanOperationRequest(base *url.URL, args []string) (projecthttp.Request, error) {
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return projecthttp.Request{}, errors.New(operationsUsage)
	}
	project := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]))
	ledger := project.JoinPath("operations")
	switch args[0] {
	case "budget":
		if len(args) == 3 {
			return projecthttp.Request{Target: project.JoinPath("operation-budget"), Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	case "set-budget":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		revision := flags.Int64("if-match", 0, "")
		if flags.Parse(args[3:]) == nil && flags.NArg() == 0 && *revision > 0 {
			return projecthttp.Request{Target: project.JoinPath("operation-budget"), Method: http.MethodPut, Status: http.StatusOK,
				Match: strconv.Quote(strconv.FormatInt(*revision, 10)), InputLimit: 1024}, nil
		}
	case "backlog":
		if query, ok := operationhttp.BacklogClientQuery(args[3:]); ok {
			target := project.JoinPath("operation-backlog")
			target.RawQuery = query.Encode()
			return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	case "latency":
		if query, ok := operationhttp.LatencyClientQuery(args[3:]); ok {
			target := project.JoinPath("operation-latency")
			target.RawQuery = query.Encode()
			return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	case "list", "get", "events":
		target, err := readOperationTarget(ledger, args)
		return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, err
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
	case "update-dns-record":
		return planOperationSubmission(project.JoinPath("dns-zones", "record-update"), args[0], args[3:])
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
			return projecthttp.Request{Target: project.JoinPath("bare-metal", "deployments"),
				Method: http.MethodPost, Status: http.StatusCreated, InputLimit: 2048}, nil
		}
	case "get-deployment", "report-deployment":
		if len(args) == 4 && models.ValidRequestSegment(args[3]) {
			target := project.JoinPath("bare-metal", "deployments", url.PathEscape(args[3]))
			if args[0] == "get-deployment" {
				return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
			}
			return projecthttp.Request{Target: target.JoinPath("receipt"), Method: http.MethodPost,
				Status: http.StatusOK, InputLimit: 2048}, nil
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
	case "create-volume":
		return planOperationSubmission(project.JoinPath("volumes"), args[0], args[3:])
	case "release-volume":
		return planOperationSubmission(project.JoinPath("volumes", "release"), args[0], args[3:])
	case "preview-volume-release":
		return planOperationAssetRead(project.JoinPath("volumes", "release-preview"), "asset_id", args[3:])
	case "resize-instance-options", "refresh-instance-resize-options":
		plan, err := planOperationAssetRead(project.JoinPath("instances", "resize-options"), "asset_id", args[3:])
		if args[0] == "refresh-instance-resize-options" {
			plan.Method, plan.Status = http.MethodPost, http.StatusAccepted
		}
		return plan, err
	case "enable-bucket-encryption":
		return planOperationSubmission(project.JoinPath("object-buckets", "encryption"), args[0], args[3:])
	case "set-bucket-versioning":
		return planOperationSubmission(project.JoinPath("object-buckets", "versioning"), args[0], args[3:])
	case "resize-instance":
		return planOperationSubmission(project.JoinPath("instances", "resize"), args[0], args[3:])
	case "resize-volume":
		return planOperationSubmission(project.JoinPath("volumes", "resize"), args[0], args[3:])
	case "rename-database-instance":
		return planOperationSubmission(project.JoinPath("databases", "rename"), args[0], args[3:])
	case "recover-database-instance":
		return planOperationSubmission(project.JoinPath("databases", "recover"), args[0], args[3:])
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
			return projecthttp.Request{Target: project.JoinPath("operation-batches", "preview"), Method: http.MethodPost,
				Status: http.StatusOK, InputLimit: operationhttp.BatchBodyLimit}, nil
		}
	case "submit-batch":
		return planOperationSubmission(project.JoinPath("operation-batches"), args[0], args[3:])
	case "get-batch":
		if len(args) == 4 && models.ValidRequestSegment(args[3]) {
			return projecthttp.Request{Target: project.JoinPath("operation-batches", url.PathEscape(args[3])),
				Method: http.MethodGet, Status: http.StatusOK}, nil
		}
	case "approve", "resolve", "retry-observation":
		if len(args) < 4 || !models.ValidRequestSegment(args[3]) {
			return projecthttp.Request{}, errors.New(operationsUsage)
		}
		return planOperationDecision(ledger.JoinPath(url.PathEscape(args[3])), args[0], args[4:])
	}
	return projecthttp.Request{}, errors.New(operationsUsage)
}

func planBareMetalReclaim(base *url.URL, command string, args []string) (projecthttp.Request, error) {
	if len(args) != 1 || !models.ValidRequestSegment(args[0]) {
		return projecthttp.Request{}, errors.New(operationsUsage)
	}
	target := base.JoinPath(url.PathEscape(args[0]))
	if command == "get-reclaim" {
		return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
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
	return projecthttp.Request{Target: target.JoinPath(suffix), Method: http.MethodPost, Status: http.StatusOK, InputLimit: 2048}, nil
}

func planOperationAssetRead(target *url.URL, idField string, args []string) (projecthttp.Request, error) {
	flags := flag.NewFlagSet("operations asset query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account := flags.String("account", "", "")
	region := flags.String("region", "", "")
	asset := flags.String(strings.ReplaceAll(idField, "_", "-"), "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *account == "" || *region == "" || *asset == "" {
		return projecthttp.Request{}, errors.New(operationsUsage)
	}
	target.RawQuery = url.Values{"account": {*account}, "region": {*region}, idField: {*asset}}.Encode()
	return projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}, nil
}

func planOperationSubmission(target *url.URL, command string, args []string) (projecthttp.Request, error) {
	flags := flag.NewFlagSet("operations "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	key := flags.String("idempotency-key", "", "")
	var match string
	requireMatch := command == "request-reclaim" || command == "bare-metal-pxe" || command == "bare-metal-power" || command == "retire-instance" || command == "release-volume" || command == "resize-volume" || command == "resize-instance" || command == "set-bucket-versioning" || command == "enable-bucket-encryption" || command == "rename-database-instance" || command == "recover-database-instance" || command == "rename-cache-instance" || command == "scale-workload" || command == "create-dns-record" || command == "delete-dns-record" || command == "set-dns-record-status" || command == "update-dns-record" || command == "submit" || command == "delete-network" || command == "rename-load-balancer" || command == "rename-load-balancer-listener" || command == "rename-load-balancer-target-group" || command == "set-load-balancer-backend-weight" || command == "detach-load-balancer-backend" || command == "attach-load-balancer-backend" || command == "revoke-security-rule" || command == "release-elastic-ip" ||
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
		return projecthttp.Request{}, errors.New(operationsUsage)
	}
	bodyLimit := 4096
	switch command {
	case "submit-batch":
		bodyLimit = operationhttp.BatchBodyLimit
	case "request-reclaim":
		bodyLimit = 2048
	}
	return projecthttp.Request{Target: target, Method: http.MethodPost, Status: http.StatusAccepted,
		Key: *key, Match: match, InputLimit: bodyLimit}, nil
}

func planOperationDecision(target *url.URL, command string, args []string) (projecthttp.Request, error) {
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
			return projecthttp.Request{}, usage
		}
		body, path = input, "approval"
	case "resolve":
		var input operations.Resolution
		flags.StringVar(&input.Decision, "decision", "", "")
		flags.StringVar(&input.EvidenceRef, "evidence-ref", "", "")
		flags.StringVar(&input.ProviderRequestID, "provider-request-id", "", "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || input.EvidenceRef == "" ||
			(input.Decision != "succeeded" && input.Decision != "failed") {
			return projecthttp.Request{}, usage
		}
		body, path = input, "resolution"
	case "retry-observation":
		var input operations.ObservationRetry
		flags.StringVar(&input.EvidenceRef, "evidence-ref", "", "")
		flags.StringVar(&input.ProviderResourceID, "provider-resource-id", "", "")
		flags.BoolVar(&input.RepairQueue, "repair-queue", false, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || input.EvidenceRef == "" ||
			(input.ProviderResourceID != "" && !provider.ValidDNSRecordID(input.ProviderResourceID)) {
			return projecthttp.Request{}, usage
		}
		body, path = input, "retry-observation"
	default:
		return projecthttp.Request{}, usage
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return projecthttp.Request{}, err
	}
	return projecthttp.Request{Target: target.JoinPath(path), Method: http.MethodPost, Status: http.StatusOK, Body: payload}, nil
}
