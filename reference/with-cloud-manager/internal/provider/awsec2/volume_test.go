package awsec2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"with-cloud-manager/internal/provider"
)

func TestEBSVolumeChanges(t *testing.T) {
	const instanceID, volumeID = "i-00000001", "vol-00000001"
	const accountID, az = "123456789012", "us-east-1a"
	var attachCalls, detachCalls atomic.Int32
	var state atomic.Value
	state.Store("available")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Header.Get("Authorization") == "" {
			t.Errorf("unsigned fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action := r.Form.Get("Action")
		write := func(body string) {
			if _, err := fmt.Fprintf(w, "<%sResponse>%s</%sResponse>", action, body, action); err != nil {
				t.Error(err)
			}
		}
		snapshot := state.Load().(string)
		device := "/dev/sdf"
		if snapshot == "root" {
			device = "/dev/sda1"
		}
		switch action {
		case "DescribeInstances":
			var mappings string
			if snapshot == "attached" || snapshot == "root" || snapshot == "instance-only" || snapshot == "foreign" {
				mappings = fmt.Sprintf(`<blockDeviceMapping><item><deviceName>%s</deviceName><ebs><volumeId>%s</volumeId><status>attached</status></ebs></item></blockDeviceMapping>`, device, volumeID)
			}
			write(fmt.Sprintf(`<reservationSet><item><ownerId>%s</ownerId><instancesSet><item><instanceId>%s</instanceId>
				<instanceState><name>running</name></instanceState><placement><availabilityZone>%s</availabilityZone></placement>
				<rootDeviceName>/dev/sda1</rootDeviceName>%s</item></instancesSet></item></reservationSet>`, accountID, instanceID, az, mappings))
		case "DescribeVolumes":
			if snapshot == "not-found" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var attachments string
			status, zone, attachedID := "available", az, instanceID
			if snapshot == "wrong-zone" {
				zone = "us-east-1b"
			}
			if snapshot == "foreign" {
				attachedID = "i-00000002"
			}
			if snapshot == "attached" || snapshot == "root" || snapshot == "foreign" {
				status = "in-use"
				attachments = fmt.Sprintf(`<attachmentSet><item><volumeId>%s</volumeId><instanceId>%s</instanceId><device>%s</device><status>attached</status></item></attachmentSet>`, volumeID, attachedID, device)
			}
			write(fmt.Sprintf(`<volumeSet><item><volumeId>%s</volumeId><availabilityZone>%s</availabilityZone><status>%s</status>%s</item></volumeSet>`, volumeID, zone, status, attachments))
		case "AttachVolume", "DetachVolume":
			if r.Form.Get("InstanceId") != instanceID || r.Form.Get("VolumeId") != volumeID || r.Form.Get("Device") != "/dev/sdf" {
				t.Error("disk operation targeted an unexpected resource")
			}
			if action == "AttachVolume" {
				attachCalls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			detachCalls.Add(1)
			if r.Form.Get("Force") != "false" {
				t.Error("detach must explicitly avoid force")
			}
			w.Header().Set("X-Amzn-Requestid", "req-detach")
			write(`<requestId>req-detach</requestId>`)
		default:
			t.Errorf("unexpected EC2 API %s", action)
		}
	}))
	defer server.Close()
	client := &Client{account: accountID, identity: "aws:" + accountID, ec2: ec2.NewFromConfig(aws.Config{
		Region: "us-east-1", HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 3,
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.identity, Region: "us-east-1", Kind: provider.Instance}
	attach := provider.VolumeChange{InstanceID: instanceID, VolumeID: volumeID, Device: "/dev/sdf", Attach: true}
	detach := provider.VolumeChange{InstanceID: instanceID, VolumeID: volumeID}
	if _, err := client.ApplyVolume(t.Context(), scope, attach); !provider.IsFailure(err, provider.VolumeBlocked) {
		t.Fatalf("writes enabled by default: %v", err)
	}
	client.allowVolumeChanges = true
	state.Store("wrong-zone")
	if _, err := client.ApplyVolume(t.Context(), scope, attach); !provider.IsFailure(err, provider.VolumeBlocked) || attachCalls.Load() != 0 {
		t.Fatalf("cross-zone attachment sent: %v", err)
	}
	state.Store("available")
	if _, err := client.ApplyVolume(t.Context(), scope, attach); err == nil || attachCalls.Load() != 1 {
		t.Fatalf("ambiguous attachment repeated: %d %v", attachCalls.Load(), err)
	}
	state.Store("instance-only")
	if reached, err := client.ObserveVolume(t.Context(), scope, attach); reached || err != nil {
		t.Fatalf("single-sided evidence accepted: %t %v", reached, err)
	}
	state.Store("attached")
	if reached, err := client.ObserveVolume(t.Context(), scope, attach); !reached || err != nil {
		t.Fatalf("expected both sides attached: %t %v", reached, err)
	}
	state.Store("root")
	if _, err := client.ApplyVolume(t.Context(), scope, detach); !provider.IsFailure(err, provider.VolumeBlocked) || detachCalls.Load() != 0 {
		t.Fatalf("root detach attempted: %v", err)
	}
	state.Store("foreign")
	if _, err := client.ApplyVolume(t.Context(), scope, detach); !provider.IsFailure(err, provider.VolumeBlocked) {
		t.Fatalf("foreign attachment accepted: %v", err)
	}
	state.Store("attached")
	if receipt, err := client.ApplyVolume(t.Context(), scope, detach); err != nil || receipt != "req-detach" || detachCalls.Load() != 1 {
		t.Fatalf("detach receipt=%q calls=%d error=%v", receipt, detachCalls.Load(), err)
	}
	state.Store("not-found")
	if reached, err := client.ObserveVolume(t.Context(), scope, detach); reached || err == nil {
		t.Fatalf("missing volume was treated as detached: %t %v", reached, err)
	}
	state.Store("available")
	if reached, err := client.ObserveVolume(t.Context(), scope, detach); !reached || err != nil {
		t.Fatalf("expected available detached volume: %t %v", reached, err)
	}
}
