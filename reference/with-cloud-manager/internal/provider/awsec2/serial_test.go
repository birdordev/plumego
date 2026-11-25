package awsec2

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"with-cloud-manager/internal/provider"
)

func TestAWSSerialConsoleScopeAndSignedDispatch(t *testing.T) {
	const owner, region = "123456789012", "us-east-1"
	const instanceID, hostname = "i-0123456789abcdef0", "serial-console.ec2-instance-connect.us-east-1.aws"
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, field := range [][]byte{[]byte("ssh-rsa"), {1, 0, 1}, append([]byte{0}, privateKey.N.Bytes()...)} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(field)))
		blob = append(blob, field...)
	}
	key := "ssh-rsa " + base64.StdEncoding.EncodeToString(blob)
	var writes, mode atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Amz-Target") != "" {
			writes.Add(1)
			var data struct {
				InstanceID   string `json:"InstanceId"`
				SerialPort   *int
				SSHPublicKey string
			}
			err := json.NewDecoder(r.Body).Decode(&data)
			if err != nil || data.InstanceID != instanceID || data.SerialPort == nil || *data.SerialPort != 0 || data.SSHPublicKey != key ||
				r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("Content-Type") != "application/x-amz-json-1.1" ||
				r.Header.Get("X-Amz-Target") != "AWSEC2InstanceConnectService.SendSerialConsoleSSHPublicKey" ||
				!strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2-instance-connect/aws4_request") ||
				r.Header.Get("X-Amz-Security-Token") != "fixture-temporary-token" {
				t.Error("unexpected signed EC2 Instance Connect request")
			}
			switch mode.Load() {
			case 3:
				w.WriteHeader(http.StatusServiceUnavailable)
			case 4:
				_, err = fmt.Fprint(w, `{"Success":true}`)
			case 5:
				w.WriteHeader(http.StatusBadRequest)
			default:
				_, err = fmt.Fprint(w, `{"Success":true,"RequestId":"fixture-serial-1"}`)
			}
			if err != nil {
				t.Error(err)
			}
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("Action") != "DescribeInstances" || r.Form.Get("InstanceId.1") != instanceID ||
			!strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
			t.Error("unexpected EC2 point lookup")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		account, state := owner, "running"
		switch mode.Load() {
		case 1:
			account = "999999999999"
		case 2:
			state = "stopped"
		}
		if _, err := fmt.Fprintf(w, `<DescribeInstancesResponse><reservationSet><item><ownerId>%s</ownerId>
			 <instancesSet><item><instanceId>%s</instanceId><instanceState><name>%s</name></instanceState></item></instancesSet>
			 </item></reservationSet></DescribeInstancesResponse>`, account, instanceID, state); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	credential := credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", "fixture-temporary-token")
	ec2Client := ec2.NewFromConfig(aws.Config{Region: region, HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), Credentials: credential})
	c := &Client{account: owner, identity: "aws:" + owner, ec2: ec2Client}
	scope := provider.Scope{Provider: Name, RemoteIdentity: c.Identity(), Region: region, Kind: provider.Instance}
	ctx := t.Context()
	if _, err := c.PrepareSerialConsole(ctx, scope, instanceID, key); !provider.IsFailure(err, provider.Unsupported) {
		t.Fatalf("serial access enabled without a profile: %v", err)
	}
	c.serial = &serialConsole{endpoint: server.URL + "/", host: hostname, region: region, http: server.Client(), credentials: credential}
	for _, negative := range []struct {
		mode int32
		kind provider.FailureKind
	}{{1, provider.InvalidResponse}, {2, provider.Unsupported}} {
		mode.Store(negative.mode)
		if _, err := c.PrepareSerialConsole(ctx, scope, instanceID, key); !provider.IsFailure(err, negative.kind) {
			t.Fatalf("invalid AWS instance accepted: %v", err)
		}
	}
	foreignRegion := scope
	foreignRegion.Region = "us-west-2"
	if _, err := c.PrepareSerialConsole(ctx, foreignRegion, instanceID, key); !provider.IsFailure(err, provider.Unsupported) || writes.Load() != 0 {
		t.Fatalf("unverified target reached AWS serial endpoint: %v", err)
	}
	mode.Store(0)
	dispatch, err := c.PrepareSerialConsole(ctx, scope, instanceID, key+" workstation-comment\n")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	receipt, err := dispatch(ctx)
	if err != nil || receipt.Port != 22 || receipt.Host != hostname || receipt.Username != instanceID+".port0" ||
		receipt.RequestID != "fixture-serial-1" || receipt.ValidUntil.Before(start.Add(time.Minute)) ||
		receipt.ValidUntil.After(time.Now().Add(time.Minute)) || writes.Load() != 1 {
		t.Fatalf("invalid SSH receipt: %+v, %v", receipt, err)
	}
	for _, failure := range []struct {
		mode int32
		kind provider.FailureKind
	}{{3, provider.OutcomeUnknown}, {4, provider.OutcomeUnknown}, {5, provider.Unauthorized}} {
		mode.Store(failure.mode)
		count := writes.Load()
		d, err := c.PrepareSerialConsole(ctx, scope, instanceID, key)
		if err != nil {
			t.Fatal(err)
		}
		if receipt, err := d(ctx); receipt.RequestID != "" || !provider.IsFailure(err, failure.kind) || writes.Load() != count+1 {
			t.Fatalf("unexpected dispatch retry or success: %v", err)
		}
	}
}

func TestAWSSerialProfileEndpoint(t *testing.T) {
	for _, test := range []struct{ partition, region, host, endpoint string }{
		{"aws", "us-east-1", "serial-console.ec2-instance-connect.us-east-1.aws", "https://ec2-instance-connect.us-east-1.amazonaws.com/"},
		{"aws", "ap-east-1", "ec2-serial-console.ap-east-1.api.aws", "https://ec2-instance-connect.ap-east-1.amazonaws.com/"},
		{"aws-us-gov", "us-gov-east-1", "serial-console.ec2-instance-connect.us-gov-east-1.amazonaws.com", "https://ec2-instance-connect.us-gov-east-1.amazonaws.com/"},
		{"aws-cn", "cn-north-1", "ec2-serial-console.cn-north-1.api.amazonwebservices.com.cn", "https://ec2-instance-connect.cn-north-1.amazonaws.com.cn/"},
		{"aws", "us-east-1", "serial-console.ec2-instance-connect.us-west-2.aws", ""},
		{"aws", "us-east-1", "https://serial-console.ec2-instance-connect.us-east-1.aws", ""},
		{"aws", "cn-north-1", "ec2-serial-console.cn-north-1.api.aws", ""},
		{"aws-unsupported", "us-east-1", "serial-console.ec2-instance-connect.us-east-1.aws", ""},
	} {
		got, err := serialAPIEndpoint(test.partition, test.region, test.host)
		if (err != nil) != (test.endpoint == "") || got != test.endpoint {
			t.Fatalf("incorrect partition and region binding for %s %s", test.partition, test.region)
		}
	}
}
