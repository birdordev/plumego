package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/provider"
)

const nativeUsage = `usage: cloud-manager console-grants COMMAND TENANT PROJECT [OPTIONS]
  list [--after GRANT_ID] [--limit COUNT]
  get GRANT_ID
  request-serial --account ACCOUNT --region REGION --asset-id INSTANCE --purpose PURPOSE --if-match REVISION --idempotency-key KEY --public-key FILE
  activate-serial GRANT_ID --if-match REVISION --public-key FILE
  approve GRANT_ID --if-match REVISION
  reject GRANT_ID --if-match REVISION
  revoke GRANT_ID --if-match REVISION`

func runNativeClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	if len(args) < 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return errors.New(nativeUsage)
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "console-grants")
	plan, err := planNativeRequest(target, args[0], args[3:])
	if err != nil {
		return err
	}
	return runProjectRequest(ctx, plan, token, nil, output, transport)
}

func planNativeRequest(target *url.URL, command string, args []string) (operationRequestPlan, error) {
	usage := errors.New(nativeUsage)
	plan := operationRequestPlan{target: target, method: http.MethodGet, status: http.StatusOK}
	flags := flag.NewFlagSet("console-grants "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if command == "request-serial" {
		return planSerialConsoleRequest(plan, flags, args, usage)
	}
	if command == "list" {
		after := flags.String("after", "", "")
		limit := flags.Int("limit", 50, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
			return operationRequestPlan{}, usage
		}
		target.RawQuery = url.Values{"after": {*after}, "limit": {strconv.Itoa(*limit)}}.Encode()
		return plan, nil
	}
	if len(args) < 1 || !operationClientSegment(args[0]) {
		return operationRequestPlan{}, usage
	}
	plan.target = target.JoinPath(url.PathEscape(args[0]))
	switch command {
	case "get":
		if len(args) == 1 {
			return plan, nil
		}
	case "activate-serial":
		revision := flags.Int64("if-match", 0, "")
		file := flags.String("public-key", "", "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *revision < 1 || *file == "" {
			return operationRequestPlan{}, usage
		}
		key, err := readSerialPublicKey(*file)
		if err != nil {
			return operationRequestPlan{}, err
		}
		plan.method = http.MethodPost
		plan.target = plan.target.JoinPath("serial-key")
		plan.match = strconv.Quote(strconv.FormatInt(*revision, 10))
		plan.timeout = 25 * time.Second
		plan.body, err = json.Marshal(serialKeyInput{SSHPublicKey: key})
		return plan, err
	case "approve", "reject", "revoke":
		revision := flags.Int64("if-match", 0, "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *revision < 1 {
			return operationRequestPlan{}, usage
		}
		plan.method = http.MethodPost
		plan.match = strconv.Quote(strconv.FormatInt(*revision, 10))
		if command == "revoke" {
			plan.target = plan.target.JoinPath("revoke")
			return plan, nil
		}
		plan.target = plan.target.JoinPath("approval")
		payload, err := json.Marshal(map[string]string{"decision": command})
		plan.body = payload
		return plan, err
	}
	return operationRequestPlan{}, usage
}

func planSerialConsoleRequest(plan operationRequestPlan, flags *flag.FlagSet, args []string, usage error) (operationRequestPlan, error) {
	var request nativeconsole.Request
	flags.StringVar(&request.Account, "account", "", "")
	flags.StringVar(&request.Region, "region", "", "")
	flags.StringVar(&request.AssetID, "asset-id", "", "")
	flags.StringVar(&request.Purpose, "purpose", "", "")
	revision := flags.Int64("if-match", 0, "")
	idempotency := flags.String("idempotency-key", "", "")
	file := flags.String("public-key", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *revision < 1 || *file == "" || *idempotency == "" ||
		request.Account == "" || request.Region == "" || request.AssetID == "" || request.Purpose == "" {
		return operationRequestPlan{}, usage
	}
	key, err := readSerialPublicKey(*file)
	if err != nil {
		return operationRequestPlan{}, err
	}
	request.SSHPublicKey = key
	plan.method, plan.status = http.MethodPost, http.StatusCreated
	plan.match = strconv.Quote(strconv.FormatInt(*revision, 10))
	plan.key = *idempotency
	plan.body, err = json.Marshal(request)
	return plan, err
}

func readSerialPublicKey(path string) (string, error) {
	const maxKey = 4096
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxKey {
		return "", errors.New("public key must be a regular OpenSSH RSA public key file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot open public key file")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxKey+1))
	if err != nil || len(data) > maxKey {
		return "", errors.New("cannot read public key file")
	}
	key, _, err := provider.NormalizeSerialPublicKey(string(data))
	if err != nil {
		return "", errors.New("invalid OpenSSH RSA public key")
	}
	return key, nil
}
