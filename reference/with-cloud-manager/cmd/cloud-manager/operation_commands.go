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
)

type operationRequestPlan struct {
	target     *url.URL
	method     string
	status     int
	key        string
	match      string
	body       []byte
	inputLimit int
	csv        bool
	timeout    time.Duration
}

func planOperationRequest(base *url.URL, args []string) (operationRequestPlan, error) {
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	project := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]))
	ledger := project.JoinPath("operations")
	switch args[0] {
	case "list", "get", "events":
		target, err := readOperationTarget(ledger, args)
		return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, err
	case "preview-delete":
		return planOperationPreview(project.JoinPath("instance-operations", "deletion-preview"), args[3:])
	case "submit":
		return planOperationSubmission(project.JoinPath("instance-operations"), args[0], args[3:])
	case "create-instance":
		return planOperationSubmission(project.JoinPath("instances"), args[0], args[3:])
	case "create-network":
		return planOperationSubmission(project.JoinPath("network-operations"), args[0], args[3:])
	case "revoke-security-rule":
		return planOperationSubmission(project.JoinPath("network-operations", "revoke-rule"), args[0], args[3:])
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

func planOperationPreview(target *url.URL, args []string) (operationRequestPlan, error) {
	flags := flag.NewFlagSet("operations preview-delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account := flags.String("account", "", "")
	region := flags.String("region", "", "")
	asset := flags.String("asset-id", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *account == "" || *region == "" || *asset == "" {
		return operationRequestPlan{}, errors.New(operationsUsage)
	}
	target.RawQuery = url.Values{"account": {*account}, "region": {*region}, "asset_id": {*asset}}.Encode()
	return operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}, nil
}

func planOperationSubmission(target *url.URL, command string, args []string) (operationRequestPlan, error) {
	flags := flag.NewFlagSet("operations "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	key := flags.String("idempotency-key", "", "")
	var match string
	requireMatch := command == "submit" || command == "revoke-security-rule"
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
	if command == "submit-batch" {
		bodyLimit = batchBodyLimit
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
		if flags.Parse(args) != nil || flags.NArg() != 0 || (input.Decision != "approve" && input.Decision != "reject") {
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
		if flags.Parse(args) != nil || flags.NArg() != 0 || input.EvidenceRef == "" {
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
