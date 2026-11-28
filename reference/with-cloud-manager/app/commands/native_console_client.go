package commands

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

	"with-cloud-manager/app/clients/projecthttp"
	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/models"
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

func RunNativeClient(ctx context.Context, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	base, err := projecthttp.BaseURL(address)
	if err != nil {
		return err
	}
	if len(args) < 3 || !models.ValidRequestSegment(args[1]) || !models.ValidRequestSegment(args[2]) {
		return errors.New(nativeUsage)
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(args[1]), "projects", url.PathEscape(args[2]), "console-grants")
	plan, err := planNativeRequest(target, args[0], args[3:])
	if err != nil {
		return err
	}
	return projecthttp.Run(ctx, plan, token, nil, output, transport)
}

func planNativeRequest(target *url.URL, command string, args []string) (projecthttp.Request, error) {
	usage := errors.New(nativeUsage)
	plan := projecthttp.Request{Target: target, Method: http.MethodGet, Status: http.StatusOK}
	flags := flag.NewFlagSet("console-grants "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	if command == "request-serial" {
		return planSerialConsoleRequest(plan, flags, args, usage)
	}
	if command == "list" {
		after := flags.String("after", "", "")
		limit := flags.Int("limit", 50, "")
		if flags.Parse(args) != nil || flags.NArg() != 0 || *limit < 1 || *limit > 100 {
			return projecthttp.Request{}, usage
		}
		target.RawQuery = url.Values{"after": {*after}, "limit": {strconv.Itoa(*limit)}}.Encode()
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
	case "activate-serial":
		revision := flags.Int64("if-match", 0, "")
		file := flags.String("public-key", "", "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *revision < 1 || *file == "" {
			return projecthttp.Request{}, usage
		}
		key, err := readSerialPublicKey(*file)
		if err != nil {
			return projecthttp.Request{}, err
		}
		plan.Method = http.MethodPost
		plan.Target = plan.Target.JoinPath("serial-key")
		plan.Match = strconv.Quote(strconv.FormatInt(*revision, 10))
		plan.Timeout = 25 * time.Second
		plan.Body, err = json.Marshal(consolehttp.SerialKeyInput{SSHPublicKey: key})
		return plan, err
	case "approve", "reject", "revoke":
		revision := flags.Int64("if-match", 0, "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *revision < 1 {
			return projecthttp.Request{}, usage
		}
		plan.Method = http.MethodPost
		plan.Match = strconv.Quote(strconv.FormatInt(*revision, 10))
		if command == "revoke" {
			plan.Target = plan.Target.JoinPath("revoke")
			return plan, nil
		}
		plan.Target = plan.Target.JoinPath("approval")
		payload, err := json.Marshal(map[string]string{"decision": command})
		plan.Body = payload
		return plan, err
	}
	return projecthttp.Request{}, usage
}

func planSerialConsoleRequest(plan projecthttp.Request, flags *flag.FlagSet, args []string, usage error) (projecthttp.Request, error) {
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
		return projecthttp.Request{}, usage
	}
	key, err := readSerialPublicKey(*file)
	if err != nil {
		return projecthttp.Request{}, err
	}
	request.SSHPublicKey = key
	plan.Method, plan.Status = http.MethodPost, http.StatusCreated
	plan.Match = strconv.Quote(strconv.FormatInt(*revision, 10))
	plan.Key = *idempotency
	plan.Body, err = json.Marshal(request)
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
