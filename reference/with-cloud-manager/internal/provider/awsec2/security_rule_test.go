package awsec2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"with-cloud-manager/internal/provider"
)

func TestEC2SecurityRuleDiscovery(t *testing.T) {
	const group, firstID, secondID = "sg-01234567", "sgr-01234567", "sgr-01234568"
	var requests atomic.Int32
	var mode atomic.Value
	mode.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("Action") != "DescribeSecurityGroupRules" ||
			!strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
			t.Errorf("invalid signed security rule discovery: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mode := mode.Load().(string)
		owner := "123456789012"
		if mode == "foreign-owner" {
			owner = "555555555555"
		}
		var id, fields, next string
		if point := r.Form.Get("SecurityGroupRuleId.1"); point != "" {
			if point != secondID || r.Form.Get("MaxResults") != "" || r.Form.Get("NextToken") != "" {
				t.Error("incorrect scoped point lookup")
			}
			id, fields = point, "<isEgress>true</isEgress><ipProtocol>-1</ipProtocol><cidrIpv6>::/0</cidrIpv6>"
			if mode == "missing" {
				w.WriteHeader(http.StatusBadRequest)
				_, err := fmt.Fprint(w, "<Response><Errors><Error><Code>InvalidSecurityGroupRuleId.NotFound</Code></Error></Errors></Response>")
				if err != nil {
					t.Error(err)
				}
				return
			}
		} else if r.Form.Get("MaxResults") != "100" {
			t.Error("discovery omitted page size")
		} else if r.Form.Get("NextToken") == "next-rules" {
			id, fields = secondID, "<isEgress>true</isEgress><ipProtocol>-1</ipProtocol><cidrIpv6>::/0</cidrIpv6>"
		} else {
			id, next = firstID, "<nextToken>next-rules</nextToken>"
			fields = "<isEgress>false</isEgress><ipProtocol>tcp</ipProtocol><fromPort>443</fromPort><toPort>443</toPort><cidrIpv4>192.0.2.5/24</cidrIpv4>"
		}
		if mode == "ambiguous-target" {
			fields += "<prefixListId>pl-01234567</prefixListId>"
		}
		_, err := fmt.Fprintf(w, "<DescribeSecurityGroupRulesResponse><securityGroupRuleSet><item>"+
			"<securityGroupRuleId>%s</securityGroupRuleId><groupId>%s</groupId><groupOwnerId>%s</groupOwnerId>%s"+
			"</item></securityGroupRuleSet>%s</DescribeSecurityGroupRulesResponse>", id, group, owner, fields, next)
		if err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := &Client{account: "123456789012", identity: "aws:123456789012", ec2: ec2.NewFromConfig(aws.Config{
		Region: "us-east-1", HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL),
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.identity, Region: "us-east-1", Kind: provider.SecurityGroupRule}
	first, err := client.Discover(t.Context(), scope, "")
	if err != nil || first.Next != "next-rules" || len(first.Objects) != 1 || first.Objects[0].ID != firstID ||
		first.Objects[0].Name != "ingress tcp 443-443 192.0.2.0/24" || first.Objects[0].Tags != nil {
		t.Fatalf("first page %+v: %v", first, err)
	}
	if parent, ok := provider.SecurityRuleParent(first.Objects[0]); !ok || parent != group {
		t.Fatalf("missing parent group in observation: %v", first.Objects)
	}
	last, err := client.Discover(t.Context(), scope, first.Next)
	if err != nil || last.Next != "" || len(last.Objects) != 1 || last.Objects[0].ID != secondID || last.Objects[0].Name != "egress any IPv6 ::/0" {
		t.Fatalf("second page %+v: %v", last, err)
	}
	if observed, err := client.Observe(t.Context(), scope, secondID); err != nil || observed.Name != last.Objects[0].Name {
		t.Fatalf("inconsistent point observation %+v: %v", observed, err)
	}
	mode.Store("foreign-owner")
	if page, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.Unauthorized) || len(page.Objects) != 0 {
		t.Fatalf("foreign rule published %+v: %v", page, err)
	}
	mode.Store("ambiguous-target")
	if _, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("ambiguous remote selector accepted: %v", err)
	}
	mode.Store("missing")
	if _, err := client.Observe(t.Context(), scope, secondID); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("ambiguous AWS NotFound was treated as authoritative: %v", err)
	}
	before := requests.Load()
	scope.RemoteIdentity = "aws:555555555555"
	if _, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.Unauthorized) || requests.Load() != before {
		t.Fatalf("wrong scope queried a real endpoint: %d %v", requests.Load(), err)
	}
}

func TestEC2SecurityGroupRuleCreation(t *testing.T) {
	const owner, region, group, ruleID = "123456789012", "us-east-1", "sg-12345678", "sgr-12345678"
	const token, cidr = "FixtureRuleIntent1234", "192.0.2.0/24"
	var writes atomic.Int32
	var fixture atomic.Value
	fixture.Store("normal")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || !strings.Contains(r.Header.Get("Authorization"), "/"+region+"/ec2/aws4_request") {
			t.Errorf("invalid signed request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		action, mode := r.Form.Get("Action"), fixture.Load().(string)
		actualOwner, actualCIDR, actualMarker := owner, cidr, token
		if mode == "foreign" {
			actualOwner = "111122223333"
		}
		if mode == "wrong-cidr" {
			actualCIDR = "192.0.2.1/32"
		}
		if mode == "other-marker" {
			actualMarker = "unrelated"
		}
		reply := func(body string) {
			if _, err := fmt.Fprintf(w, "<%sResponse>%s</%sResponse>", action, body, action); err != nil {
				t.Error(err)
			}
		}
		switch action {
		case "DescribeSecurityGroups":
			if r.Form.Get("GroupId.1") != group {
				t.Error("did not verify the exact parent group")
			}
			reply(fmt.Sprintf("<securityGroupInfo><item><groupId>%s</groupId><groupName>private</groupName><ownerId>%s</ownerId></item></securityGroupInfo>", group, actualOwner))
		case "AuthorizeSecurityGroupIngress", "AuthorizeSecurityGroupEgress":
			writes.Add(1)
			for key, want := range map[string]string{
				"GroupId": group, "IpPermissions.1.IpProtocol": "tcp", "IpPermissions.1.FromPort": "443", "IpPermissions.1.ToPort": "444",
				"IpPermissions.1.IpRanges.1.CidrIp": cidr, "TagSpecification.1.ResourceType": "security-group-rule",
				"TagSpecification.1.Tag.1.Key": networkIntentTag, "TagSpecification.1.Tag.1.Value": token,
			} {
				if r.Form.Get(key) != want {
					t.Errorf("incorrect rule write field %s: got %q want %q", key, r.Form.Get(key), want)
				}
			}
			if mode == "lost-response" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if action != "AuthorizeSecurityGroupEgress" {
				t.Error("wrong direction on positive write")
			}
			w.Header().Set("X-Amzn-Requestid", "req-rule")
			reply("<return>true</return><securityGroupRuleSet><item><securityGroupRuleId>" + ruleID + "</securityGroupRuleId></item></securityGroupRuleSet>")
		case "DescribeSecurityGroupRules":
			if r.Form.Get("SecurityGroupRuleId.1") != ruleID && (r.Form.Get("Filter.1.Name") != "tag:"+networkIntentTag ||
				r.Form.Get("Filter.1.Value.1") != token || r.Form.Get("Filter.2.Name") != "group-id" || r.Form.Get("Filter.2.Value.1") != group) {
				t.Error("rule lookup was not scoped to the marker and group")
			}
			row := fmt.Sprintf("<item><securityGroupRuleId>%s</securityGroupRuleId><groupId>%s</groupId><groupOwnerId>%s</groupOwnerId>"+
				"<isEgress>false</isEgress><ipProtocol>tcp</ipProtocol><fromPort>443</fromPort><toPort>444</toPort><cidrIpv4>%s</cidrIpv4>"+
				"<tagSet><item><key>%s</key><value>%s</value></item></tagSet></item>", ruleID, group, actualOwner, actualCIDR, networkIntentTag, actualMarker)
			if mode == "duplicate" {
				row += row
			}
			reply("<securityGroupRuleSet>" + row + "</securityGroupRuleSet>")
		default:
			t.Errorf("unexpected EC2 action %q", action)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := &Client{account: owner, identity: "aws:" + owner, allowSecurityGroupCreation: true, ec2: ec2.NewFromConfig(aws.Config{
		Region: region, HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 3,
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.identity, Region: region, Kind: provider.SecurityGroupRule}
	spec := provider.NetworkSpec{ParentID: group, Rule: &provider.SecurityRule{Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 444, RemoteCIDR: cidr}}
	ctx := t.Context()
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("group creation enabled rule changes: %v", err)
	}
	client.allowSecurityGroupRules = true
	fixture.Store("foreign")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); !provider.IsFailure(err, provider.CreationBlocked) || writes.Load() != 0 {
		t.Fatalf("rule reached a foreign parent: %v", err)
	}
	fixture.Store("lost-response")
	if _, err := client.CreateNetwork(ctx, scope, spec, token); err == nil || writes.Load() != 1 {
		t.Fatalf("unexpected ambiguous send count %d error=%v", writes.Load(), err)
	}
	if got, err := client.FindCreatedNetwork(ctx, scope, spec, token, ""); err != nil || got.ID != ruleID ||
		len(got.Links) != 1 || got.Links[0].ID != group || got.Links[0].Kind != provider.SecurityGroup {
		t.Fatalf("marker recovery failed: %+v %v", got, err)
	}
	for _, mode := range []string{"foreign", "wrong-cidr", "other-marker", "duplicate"} {
		fixture.Store(mode)
		if _, err := client.FindCreatedNetwork(ctx, scope, spec, token, ruleID); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("%s evidence was accepted: %v", mode, err)
		}
	}
	fixture.Store("normal")
	spec.Rule.Direction = "egress"
	if got, err := client.CreateNetwork(ctx, scope, spec, token); err != nil || got.ResourceID != ruleID || got.RequestID != "req-rule" || writes.Load() != 2 {
		t.Fatalf("egress receipt=%+v writes=%d error=%v", got, writes.Load(), err)
	}
}
