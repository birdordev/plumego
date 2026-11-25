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

func TestEC2NetworkCreation(t *testing.T) {
	const owner, region = "123456789012", "us-east-1"
	const token, networkID, subnetID = "NewNetworkIntent233", "vpc-12345678", "subnet-23456789"
	const networkCIDR, subnetCIDR = "10.10.0.0/16", "10.10.56.0/24"
	tags := fmt.Sprintf("<tagSet><item><key>Name</key><value>fixture</value></item><item><key>%s</key><value>%s</value></item></tagSet>", networkIntentTag, token)
	var networkWrites, subnetWrites atomic.Int32
	var mode atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Header.Get("Authorization") == "" {
			t.Errorf("invalid signed request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action := r.Form.Get("Action")
		respond := func(body string) {
			if _, err := fmt.Fprintf(w, "<%sResponse>%s</%sResponse>", action, body, action); err != nil {
				t.Error(err)
			}
		}
		actualOwner := owner
		if mode.Load() == 1 {
			actualOwner = "999999999999"
		}
		switch action {
		case "CreateVpc", "CreateSubnet":
			kind, cidr := "vpc", networkCIDR
			if action == "CreateSubnet" {
				kind, cidr = "subnet", subnetCIDR
				subnetWrites.Add(1)
				if r.Form.Get("VpcId") != networkID {
					t.Error("invalid parent")
				}
			} else {
				networkWrites.Add(1)
			}
			if r.Form.Get("TagSpecification.1.ResourceType") != kind || r.Form.Get("CidrBlock") != cidr ||
				r.Form.Get("TagSpecification.1.Tag.1.Key") != "Name" || r.Form.Get("TagSpecification.1.Tag.1.Value") != "fixture" ||
				r.Form.Get("TagSpecification.1.Tag.2.Key") != networkIntentTag || r.Form.Get("TagSpecification.1.Tag.2.Value") != token {
				t.Error("expected atomic tag and CIDR")
			}
			if kind == "vpc" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("X-Amzn-Requestid", "req-subnet")
			respond("<subnet><subnetId>" + subnetID + "</subnetId></subnet>")
		case "DescribeVpcs":
			filter := r.Form.Get("Filter.1.Name")
			if filter != "" && (filter != "tag:"+networkIntentTag || r.Form.Get("Filter.1.Value.1") != token) {
				t.Error("invalid correlation filter")
			}
			respond(fmt.Sprintf("<vpcSet><item><vpcId>%s</vpcId><ownerId>%s</ownerId><cidrBlock>%s</cidrBlock><state>available</state>%s</item></vpcSet>",
				networkID, actualOwner, networkCIDR, tags))
		case "DescribeSubnets":
			next := ""
			if mode.Load() == 2 {
				next = "<nextToken>another-page</nextToken>"
			}
			respond(fmt.Sprintf("<subnetSet><item><subnetId>%s</subnetId><vpcId>%s</vpcId><ownerId>%s</ownerId><cidrBlock>%s</cidrBlock><state>available</state>%s</item></subnetSet>%s",
				subnetID, networkID, actualOwner, subnetCIDR, tags, next))
		default:
			t.Errorf("unexpected action %q", action)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := &Client{account: owner, identity: "aws:" + owner, ec2: ec2.NewFromConfig(aws.Config{
		Region: region, HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 3,
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	ctx := t.Context()
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.Identity(), Region: region, Kind: provider.Network}
	network := provider.NetworkSpec{Name: "fixture", CIDR: networkCIDR}
	if _, err := client.CreateNetwork(ctx, scope, network, token); !provider.IsFailure(err, provider.CreationBlocked) || networkWrites.Load() != 0 {
		t.Fatalf("writes enabled by default: %v", err)
	}
	client.allowNetworkCreation = true
	if _, err := client.CreateNetwork(ctx, scope, network, token); err == nil || networkWrites.Load() != 1 {
		t.Fatalf("ambiguous VPC create writes=%d error=%v", networkWrites.Load(), err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, network, token, ""); err != nil || obj.ID != networkID || obj.Tags[networkIntentTag] != "" {
		t.Fatalf("invalid VPC proof: %+v %v", obj, err)
	}
	scope.Kind = provider.Subnet
	subnet := provider.NetworkSpec{Name: "fixture", CIDR: subnetCIDR, ParentID: networkID}
	if _, err := client.CreateNetwork(ctx, scope, provider.NetworkSpec{Name: "fixture", CIDR: "10.11.0.0/24", ParentID: networkID}, token); !provider.IsFailure(err, provider.CreationBlocked) {
		t.Fatalf("subnet outside VPC submitted: %v", err)
	}
	mode.Store(1)
	if _, err := client.CreateNetwork(ctx, scope, subnet, token); !provider.IsFailure(err, provider.CreationBlocked) || subnetWrites.Load() != 0 {
		t.Fatalf("foreign VPC targeted: %v", err)
	}
	mode.Store(0)
	receipt, err := client.CreateNetwork(ctx, scope, subnet, token)
	if err != nil || receipt.ResourceID != subnetID || receipt.RequestID != "req-subnet" || subnetWrites.Load() != 1 {
		t.Fatalf("subnet receipt: %+v %v", receipt, err)
	}
	if obj, err := client.FindCreatedNetwork(ctx, scope, subnet, token, receipt.ResourceID); err != nil || obj.ID != subnetID ||
		len(obj.Links) != 1 || obj.Links[0].ID != networkID {
		t.Fatalf("invalid subnet proof: %+v %v", obj, err)
	}
	mode.Store(2)
	if _, err := client.FindCreatedNetwork(ctx, scope, subnet, token, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("ambiguous subnet search accepted: %v", err)
	}
}
