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

func TestEC2SecurityGroupCreation(t *testing.T) {
	const owner, region = "123456789012", "us-east-1"
	const token, vpcID, groupID = "FixturePrivateGroupToken22", "vpc-12345678", "sg-23456789"
	const name = "fixture group"
	var writes atomic.Int32
	var fixture atomic.Value
	fixture.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Header.Get("Authorization") == "" {
			t.Errorf("invalid signed request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action := r.Form.Get("Action")
		reply := func(body string) {
			if _, err := fmt.Fprintf(w, "<%sResponse>%s</%sResponse>", action, body, action); err != nil {
				t.Error(err)
			}
		}
		mode := fixture.Load().(string)
		switch action {
		case "DescribeVpcs":
			vpcOwner := owner
			if mode == "foreign-parent" {
				vpcOwner = "111122223333"
			}
			if r.Form.Get("VpcId.1") != vpcID {
				t.Error("expected scoped VPC point read")
			}
			reply(fmt.Sprintf("<vpcSet><item><vpcId>%s</vpcId><ownerId>%s</ownerId><state>available</state></item></vpcSet>", vpcID, vpcOwner))
		case "CreateSecurityGroup":
			writes.Add(1)
			if r.Form.Get("VpcId") != vpcID || r.Form.Get("GroupName") != name ||
				r.Form.Get("GroupDescription") == "" || r.Form.Get("TagSpecification.1.ResourceType") != "security-group" ||
				r.Form.Get("TagSpecification.1.Tag.2.Key") != networkIntentTag || r.Form.Get("TagSpecification.1.Tag.2.Value") != token {
				t.Error("invalid EC2 security group request")
			}
			if mode == "lost-response" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Amzn-Requestid", "req-security-group")
			reply("<groupId>" + groupID + "</groupId>")
		case "DescribeSecurityGroups":
			if r.Form.Get("GroupId.1") != groupID &&
				(r.Form.Get("Filter.1.Name") != "tag:"+networkIntentTag || r.Form.Get("Filter.1.Value.1") != token) {
				t.Error("expected security group ID or creation marker lookup")
			}
			actualOwner, parentID, actualToken := owner, vpcID, token
			ingress, continuation := "", ""
			switch mode {
			case "foreign":
				actualOwner = "222233334444"
			case "other-vpc":
				parentID = "vpc-00000000"
			case "other-marker":
				actualToken = "unrelated"
			case "ingress":
				ingress = "<ipPermissions><item><ipProtocol>tcp</ipProtocol><fromPort>443</fromPort><toPort>443</toPort></item></ipPermissions>"
			case "paginated":
				continuation = "<nextToken>another-page</nextToken>"
			}
			reply(fmt.Sprintf("<securityGroupInfo><item><groupId>%s</groupId><groupName>%s</groupName><ownerId>%s</ownerId><vpcId>%s</vpcId>"+
				"<tagSet><item><key>%s</key><value>%s</value></item></tagSet>%s</item></securityGroupInfo>%s",
				groupID, name, actualOwner, parentID, networkIntentTag, actualToken, ingress, continuation))
		default:
			t.Errorf("unexpected EC2 operation %q", action)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := &Client{account: owner, identity: "aws:" + owner, allowNetworkCreation: true, ec2: ec2.NewFromConfig(aws.Config{
		Region: region, HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 3,
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.Identity(), Region: region, Kind: provider.SecurityGroup}
	spec := provider.NetworkSpec{Name: name, ParentID: vpcID}
	ctx := t.Context()
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("network opt-in enabled group change: %v", err)
	}
	client.allowSecurityGroupCreation = true
	fixture.Store("foreign-parent")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("foreign VPC sent a group create: %v", err)
	}
	fixture.Store("lost-response")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); err == nil || writes.Load() != 1 {
		t.Fatalf("ambiguous change writes=%d error=%v", writes.Load(), err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); err != nil || obj.ID != groupID ||
		obj.Status != "available" || len(obj.Links) != 1 || obj.Links[0].ID != vpcID || obj.Tags[networkIntentTag] != "" {
		t.Fatalf("invalid marker observation: %+v %v", obj, err)
	}
	fixture.Store("normal")
	receipt, err := client.CreateNetwork(ctx, scope, spec, token)
	if err != nil || receipt.ResourceID != groupID || receipt.RequestID != "req-security-group" || writes.Load() != 2 {
		t.Fatalf("invalid group receipt: %+v %v", receipt, err)
	}
	for _, mode := range []string{"foreign", "other-vpc", "other-marker", "ingress", "paginated"} {
		fixture.Store(mode)
		if _, err := client.FindCreatedNetwork(ctx, scope, spec, token, groupID); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("%s evidence was accepted: %v", mode, err)
		}
	}
}
