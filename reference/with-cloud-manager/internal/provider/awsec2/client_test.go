package awsec2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"with-cloud-manager/internal/provider"
)

func TestAWSInventoryProtocol(t *testing.T) {
	const account = "123456789012"
	const roleARN = "arn:aws:iam::123456789012:role/path/reader"
	const external = "fixture-reference"
	var failedWrites, deleteWrites atomic.Int32
	var launchWrites atomic.Int32
	const launchToken = "FixtureLaunchToken1234567890"
	spec := provider.InstanceSpec{Name: "example", ImageID: "ami-01234567", Size: "t3.micro", NetworkID: "subnet-01234567", SecurityGroup: "sg-01234567"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Header.Get("Authorization") == "" {
			t.Error("missing AWS form or signature")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action := r.Form.Get("Action")
		if action != "AssumeRole" && r.Header.Get("X-Amz-Security-Token") != "fixture-session" {
			t.Error("read used the wrong role credentials")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		write := func(body string) {
			if _, err := fmt.Fprint(w, "<"+action+"Response>"+body+"</"+action+"Response>"); err != nil {
				t.Error(err)
			}
		}
		switch action {
		case "AssumeRole":
			if r.Form.Get("ExternalId") != external || r.Form.Get("RoleArn") != roleARN {
				t.Error("unexpected assume role scope")
			}
			write(fmt.Sprintf(`<AssumeRoleResult><Credentials><AccessKeyId>FIXTURESESSIONKEY</AccessKeyId>
				<SecretAccessKey>fixture-session-signing-key</SecretAccessKey>
				<SessionToken>fixture-session</SessionToken><Expiration>%s</Expiration></Credentials></AssumeRoleResult>`,
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339)))
		case "GetCallerIdentity":
			write(`<GetCallerIdentityResult><Account>123456789012</Account>
				<Arn>arn:aws:sts::123456789012:assumed-role/reader/cloud-manager-inventory</Arn></GetCallerIdentityResult>`)
		case "StartInstances", "StopInstances", "RebootInstances":
			if r.Form.Get("InstanceId.1") == "failed-write" {
				failedWrites.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if r.Form.Get("InstanceId.1") != "i-one" {
				t.Error("incorrect AWS action target")
			}
			w.Header().Set("X-Amzn-Requestid", "fixture-aws-operation")
			write("<requestId>fixture-aws-operation</requestId><return>true</return>")
		case "TerminateInstances":
			deleteWrites.Add(1)
			switch r.Form.Get("InstanceId.1") {
			case "retain-volume":
				w.Header().Set("X-Amzn-Requestid", "fixture-aws-delete")
				write("<requestId>fixture-aws-delete</requestId>")
			case "delete-disconnected":
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				t.Error("termination sent for an unverified instance")
				w.WriteHeader(http.StatusBadRequest)
			}
		case "RunInstances":
			launchWrites.Add(1)
			for key, want := range map[string]string{
				"ClientToken": launchToken, "MinCount": "1", "MaxCount": "1", "ImageId": spec.ImageID, "InstanceType": spec.Size,
				"NetworkInterface.1.SubnetId": spec.NetworkID, "NetworkInterface.1.SecurityGroupId.1": spec.SecurityGroup,
				"NetworkInterface.1.AssociatePublicIpAddress": "false", "MetadataOptions.HttpTokens": "required",
			} {
				if got := r.Form.Get(key); got != want {
					t.Errorf("RunInstances form key %s=%q want %q", key, got, want)
				}
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		case "DescribeRegions":
			write(`<regionInfo><item><regionName>us-east-1</regionName><optInStatus>opt-in-not-required</optInStatus></item></regionInfo>`)
		case "DescribeAvailabilityZones":
			write(`<availabilityZoneInfo><item><zoneId>use1-az1</zoneId><zoneName>us-east-1a</zoneName>
				<zoneState>available</zoneState></item></availabilityZoneInfo>`)
		case "DescribeInstances":
			if r.Form.Get("Filter.1.Name") == "client-token" {
				if launchWrites.Load() == 0 {
					write(`<reservationSet/>`)
					return
				}
				if r.Form.Get("Filter.1.Value.1") != launchToken {
					t.Error("point correlation used wrong filter")
				}
				write(fmt.Sprintf(`<reservationSet><item><ownerId>%s</ownerId><instancesSet><item>
				<instanceId>i-01234567</instanceId><imageId>%s</imageId><subnetId>%s</subnetId><instanceType>%s</instanceType>
				<clientToken>%s</clientToken><instanceState><name>running</name></instanceState>
				<groupSet><item><groupId>%s</groupId></item></groupSet>
				</item></instancesSet></item></reservationSet>`, account, spec.ImageID, spec.NetworkID, spec.Size, launchToken, spec.SecurityGroup))
				return
			}
			if r.Form.Get("InstanceId.1") == "missing" || r.Form.Get("NextToken") == "expired" {
				code := "InvalidInstanceID.NotFound"
				if r.Form.Get("NextToken") == "expired" {
					code = "InvalidNextToken"
				}
				w.WriteHeader(http.StatusBadRequest)
				if _, err := fmt.Fprintf(w, "<Response><Errors><Error><Code>%s</Code></Error></Errors></Response>", code); err != nil {
					t.Error(err)
				}
				return
			}
			id := r.Form.Get("InstanceId.1")
			next := ""
			if id != "" && r.Form.Get("MaxResults") != "" {
				t.Error("point lookup combined ID and page limit")
			}
			if id == "" {
				id = "i-one"
				next = "<nextToken>page-two</nextToken>"
				if r.Form.Get("NextToken") == "page-two" {
					id, next = "i-two", ""
				}
			}
			var mappings string
			switch id {
			case "retain-volume", "delete-disconnected":
				mappings = "<deleteOnTermination>false</deleteOnTermination>"
			case "destroy-volume":
				mappings = "<deleteOnTermination>true</deleteOnTermination>"
			}
			if mappings != "" {
				mappings = `<rootDeviceType>ebs</rootDeviceType><rootDeviceName>/dev/sda1</rootDeviceName>
					<blockDeviceMapping><item><deviceName>/dev/sda1</deviceName><ebs><volumeId>vol-one</volumeId>` + mappings + `</ebs></item></blockDeviceMapping>`
			}
			write(fmt.Sprintf(`<reservationSet><item><ownerId>%s</ownerId><instancesSet><item>
				<instanceId>%s</instanceId><vpcId>vpc-one</vpcId><subnetId>subnet-one</subnetId><instanceState><name>running</name></instanceState>
				<tagSet><item><key>Name</key><value>worker</value></item></tagSet>%s
				</item></instancesSet></item></reservationSet>%s`, account, id, mappings, next))
		case "DescribeVolumes":
			write(`<volumeSet><item><volumeId>vol-one</volumeId><status>available</status></item></volumeSet>`)
		case "DescribeVpcs":
			write(`<vpcSet><item><vpcId>vpc-one</vpcId><state>available</state></item></vpcSet>`)
		case "DescribeSubnets":
			write(`<subnetSet><item><subnetId>subnet-one</subnetId><state>available</state></item></subnetSet>`)
		case "DescribeSecurityGroups":
			write(`<securityGroupInfo><item><groupId>sg-one</groupId><groupName>default</groupName></item></securityGroupInfo>`)
		case "DescribeImages":
			if !strings.Contains(r.Form.Encode(), "self") {
				t.Error("AMI listing was not limited to owned images")
			}
			write(`<imagesSet><item><imageId>ami-one</imageId><name>image</name><imageState>available</imageState></item></imagesSet>`)
		default:
			t.Errorf("unexpected AWS action %q", action)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	profile := Config{Region: "us-east-1", RoleARN: roleARN, ExternalID: external}
	role, err := arn.Parse(roleARN)
	if err != nil {
		t.Fatal(err)
	}
	cfg := aws.Config{
		Region: profile.Region, BaseEndpoint: aws.String(server.URL), HTTPClient: server.Client(),
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREORIGINKEY", "fixture-origin-signing-key", ""), RetryMaxAttempts: 3,
	}
	wrongRole := role
	wrongRole.AccountID = "123456789013"
	if _, err := connectWithConfig(t.Context(), profile, wrongRole, cfg); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("AWS identity mismatch: %v", err)
	}
	client, err := connectWithConfig(t.Context(), profile, role, cfg)
	if err != nil || client.Identity() != "aws:"+account {
		t.Fatalf("verified identity: %v", err)
	}
	for _, tc := range []struct {
		kind         provider.Kind
		region, id   string
		cursor, next string
	}{
		{kind: provider.Region, id: "us-east-1"},
		{kind: provider.Zone, region: "us-east-1", id: "use1-az1"},
		{kind: provider.Instance, region: "us-east-1", id: "i-one", next: "page-two"},
		{kind: provider.Instance, region: "us-east-1", id: "i-two", cursor: "page-two"},
		{kind: provider.Volume, region: "us-east-1", id: "vol-one"},
		{kind: provider.Network, region: "us-east-1", id: "vpc-one"},
		{kind: provider.Subnet, region: "us-east-1", id: "subnet-one"},
		{kind: provider.SecurityGroup, region: "us-east-1", id: "sg-one"},
		{kind: provider.Image, region: "us-east-1", id: "ami-one"},
	} {
		t.Run(string(tc.kind)+"/"+tc.id, func(t *testing.T) {
			scope := provider.Scope{Provider: Name, RemoteIdentity: client.Identity(), Region: tc.region, Kind: tc.kind}
			page, err := client.Discover(t.Context(), scope, tc.cursor)
			if err != nil || len(page.Objects) != 1 || page.Objects[0].ID != tc.id || page.Next != tc.next {
				t.Fatalf("AWS inventory page %+v: %v", page, err)
			}
		})
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.Identity(), Region: "us-east-1", Kind: provider.Instance}
	for _, action := range []string{"start", "stop", "reboot"} {
		if _, err := client.ApplyInstance(t.Context(), scope, "i-one", action); err != nil {
			t.Fatalf("AWS %s failed: %v", action, err)
		}
	}
	if _, err := client.ApplyInstance(t.Context(), scope, "failed-write", "start"); err == nil || failedWrites.Load() != 1 {
		t.Fatalf("AWS ambiguous write calls=%d error=%v", failedWrites.Load(), err)
	}
	if _, err := client.ApplyInstance(t.Context(), scope, "retain-volume", "delete"); !provider.IsFailure(err, provider.DeletionDisabled) || deleteWrites.Load() != 0 {
		t.Fatalf("AWS deletion enabled by default: %v", err)
	}
	profile.AllowInstanceDelete = true
	profile.InstanceCreateSizes = []string{spec.Size}
	client, err = connectWithConfig(t.Context(), profile, role, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"destroy-volume", "i-one", "missing"} {
		if _, err := client.ApplyInstance(t.Context(), scope, id, "delete"); !provider.IsFailure(err, provider.DeletionBlocked) {
			t.Fatalf("unsafe AWS deletion %s: %v", id, err)
		}
	}
	if receipt, err := client.ApplyInstance(t.Context(), scope, "retain-volume", "delete"); err != nil || receipt != "fixture-aws-delete" {
		t.Fatalf("AWS deletion receipt=%q error=%v", receipt, err)
	}
	if _, err := client.ApplyInstance(t.Context(), scope, "delete-disconnected", "delete"); err == nil || deleteWrites.Load() != 2 {
		t.Fatalf("AWS delete attempts=%d error=%v", deleteWrites.Load(), err)
	}
	if _, err := client.FindCreatedInstance(t.Context(), scope, spec, launchToken, ""); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("empty observation was accepted: %v", err)
	}
	wrongSize := spec
	wrongSize.Size = "m8g.8xlarge"
	if _, err := client.CreateInstance(t.Context(), scope, wrongSize, launchToken); !provider.IsFailure(err, provider.CreationBlocked) || launchWrites.Load() != 0 {
		t.Fatalf("unauthorized size created: %v", err)
	}
	if _, err := client.CreateInstance(t.Context(), scope, spec, launchToken); err == nil || launchWrites.Load() != 1 {
		t.Fatalf("launch timeout writes=%d error=%v", launchWrites.Load(), err)
	}
	if got, err := client.FindCreatedInstance(t.Context(), scope, spec, launchToken, "i-01234567"); err != nil || got.ID != "i-01234567" || got.Status != "running" {
		t.Fatalf("creation evidence %+v error=%v", got, err)
	}
	if _, err := client.FindCreatedInstance(t.Context(), scope, wrongSize, launchToken, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("mismatched launch spec was accepted: %v", err)
	}
	if object, err := client.Observe(t.Context(), scope, "i-one"); err != nil || object.Name != "worker" || object.Status != "running" || object.Tags["Name"] != "worker" || len(object.Links) != 2 || object.Links[0].ID != "vpc-one" {
		t.Fatalf("AWS point lookup %+v: %v", object, err)
	}
	if _, err := client.Observe(t.Context(), scope, "missing"); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("AWS 404-like result cannot confirm absence: %v", err)
	}
	if _, err := client.Discover(t.Context(), scope, "expired"); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("AWS cursor expiry: %v", err)
	}
}
