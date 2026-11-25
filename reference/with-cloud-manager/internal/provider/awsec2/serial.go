package awsec2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"with-cloud-manager/internal/provider"
)

var serialInstanceID = regexp.MustCompile(`^i-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)
var serialRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)

type serialConsole struct {
	endpoint, host, region string
	credentials            aws.CredentialsProvider
	http                   aws.HTTPClient
}

func serialAPIEndpoint(partition, region, host string) (string, error) {
	if host == "" {
		return "", nil
	}
	invalid := errors.New("AWS serial console host does not match the configured partition and region")
	if !regionName.MatchString(region) {
		return "", invalid
	}
	legacy := "serial-console.ec2-instance-connect." + region + ".aws"
	modern := "ec2-serial-console." + region + ".api.aws"
	suffix := "amazonaws.com"
	switch partition {
	case "aws":
		if strings.HasPrefix(region, "cn-") || strings.HasPrefix(region, "us-gov-") || (host != legacy && host != modern) {
			return "", invalid
		}
	case "aws-us-gov":
		if !strings.HasPrefix(region, "us-gov-") || host != "serial-console.ec2-instance-connect."+region+".amazonaws.com" {
			return "", invalid
		}
	case "aws-cn":
		suffix = "amazonaws.com.cn"
		if !strings.HasPrefix(region, "cn-") || host != "ec2-serial-console."+region+".api.amazonwebservices.com.cn" {
			return "", invalid
		}
	default:
		return "", invalid
	}
	return "https://ec2-instance-connect." + region + "." + suffix + "/", nil
}

func (c *Client) PrepareSerialConsole(ctx context.Context, scope provider.Scope, id, publicKey string) (provider.SerialDispatch, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	if c.serial == nil || scope.Kind != provider.Instance || scope.Region != c.serial.region || !serialInstanceID.MatchString(id) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	key, _, err := provider.NormalizeSerialPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	instance, err := c.Observe(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if instance.Status != "running" {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	body, err := json.Marshal(struct {
		InstanceID   string `json:"InstanceId"`
		SerialPort   int
		SSHPublicKey string
	}{InstanceID: id, SerialPort: 0, SSHPublicKey: key})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (provider.SerialReceipt, error) {
		return c.serial.send(ctx, id, body)
	}, nil
}

func (c *serialConsole) send(ctx context.Context, id string, body []byte) (provider.SerialReceipt, error) {
	credentials, err := c.credentials.Retrieve(ctx)
	if err != nil {
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	// 串口公钥写入没有厂商幂等键，请求正文不可重放。
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSEC2InstanceConnectService.SendSerialConsoleSSHPublicKey")
	hash := sha256.Sum256(body)
	sentAt := time.Now().UTC()
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, hex.EncodeToString(hash[:]), "ec2-instance-connect", c.region, sentAt); err != nil {
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 400 && resp.StatusCode <= 499 && resp.StatusCode != http.StatusRequestTimeout {
			return provider.SerialReceipt{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	const receiptLimit = 4096
	data, err := io.ReadAll(io.LimitReader(resp.Body, receiptLimit+1))
	var result struct {
		RequestID string `json:"RequestId"`
		Success   bool   `json:"Success"`
	}
	if err != nil || len(data) > receiptLimit || json.Unmarshal(data, &result) != nil || !result.Success ||
		!serialRequestID.MatchString(result.RequestID) {
		return provider.SerialReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.SerialReceipt{Host: c.host, Port: 22, Username: id + ".port0", ValidUntil: sentAt.Add(time.Minute), RequestID: result.RequestID}, nil
}
