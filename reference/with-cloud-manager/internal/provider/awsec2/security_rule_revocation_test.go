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

func TestEC2SecurityRuleRevocation(t *testing.T) {
	const groupID, ruleID, account, region = "sg-01234567", "sgr-01234567", "123456789012", "us-east-1"
	var modes atomic.Value
	modes.Store("present")
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || !strings.Contains(r.Header.Get("Authorization"), "/"+region+"/ec2/aws4_request") {
			t.Errorf("unsigned request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mode, action := modes.Load().(string), r.Form.Get("Action")
		var reply string
		switch action {
		case "DescribeSecurityGroups":
			if r.Form.Get("GroupId.1") != groupID {
				t.Error("parent lookup did not use exact group")
			}
			owner, name := account, "private"
			if mode == "foreign" {
				owner = "111122223333"
			}
			if mode == "default" {
				name = "default"
			}
			reply = fmt.Sprintf("<securityGroupInfo><item><groupId>%s</groupId><groupName>%s</groupName><ownerId>%s</ownerId></item></securityGroupInfo>", groupID, name, owner)
		case "DescribeSecurityGroupRules":
			filtered := r.Form.Get("Filter.1.Name") == "group-id"
			if filtered {
				if r.Form.Get("Filter.1.Value.1") != groupID || r.Form.Get("Filter.2.Name") != "security-group-rule-id" || r.Form.Get("Filter.2.Value.1") != ruleID {
					t.Error("filtered rule lookup lost match coordinates")
				}
			} else if r.Form.Get("SecurityGroupRuleId.1") != ruleID {
				t.Error("point read has wrong rule ID")
			}
			if mode == "paginated" && filtered {
				reply = "<securityGroupRuleSet/><nextToken>more</nextToken>"
			} else if mode == "missing" || (mode == "disagree" && filtered) {
				reply = "<securityGroupRuleSet/>"
			} else {
				cidr := "192.0.2.0/24"
				if mode == "changed" {
					cidr = "203.0.113.0/24"
				}
				reply = fmt.Sprintf("<securityGroupRuleSet><item><securityGroupRuleId>%s</securityGroupRuleId><groupId>%s</groupId>"+
					"<groupOwnerId>%s</groupOwnerId><isEgress>%t</isEgress><ipProtocol>tcp</ipProtocol><fromPort>443</fromPort><toPort>443</toPort>"+
					"<cidrIpv4>%s</cidrIpv4></item></securityGroupRuleSet>", ruleID, groupID, account, mode != "ingress", cidr)
			}
		case "RevokeSecurityGroupIngress", "RevokeSecurityGroupEgress":
			writes.Add(1)
			if r.Form.Get("GroupId") != groupID || r.Form.Get("SecurityGroupRuleId.1") != ruleID || r.Form.Get("SecurityGroupRuleId.2") != "" || r.Form.Get("IpPermissions.1.IpProtocol") != "" {
				t.Error("revoke did not send exactly the approved rule ID")
			}
			if (action == "RevokeSecurityGroupIngress") != (mode == "ingress") {
				t.Error("revocation did not use the approved direction")
			}
			if mode == "ingress" {
				w.Header().Set("X-Amzn-Requestid", "req-ingress-revoke")
				reply = "<return>true</return>"
				break
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		default:
			t.Errorf("unexpected EC2 action %q", action)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := fmt.Fprintf(w, "<%sResponse>%s</%sResponse>", action, reply, action); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := &Client{account: account, identity: "aws:" + account, allowSecurityGroupRules: true, ec2: ec2.NewFromConfig(aws.Config{
		Region: region, HTTPClient: server.Client(), BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 3,
		Credentials: credentials.NewStaticCredentialsProvider("FIXTUREKEY", "fixture-key", ""),
	})}
	scope := provider.Scope{Provider: Name, RemoteIdentity: client.identity, Region: region, Kind: provider.SecurityGroupRule}
	target := provider.SecurityRuleTarget{ID: ruleID, GroupID: groupID, Name: "egress tcp 443-443 192.0.2.0/24"}
	ctx := t.Context()
	if send, err := client.PrepareSecurityRuleRevocation(ctx, scope, target); send != nil || !provider.IsFailure(err, provider.DeletionDisabled) {
		t.Fatalf("creation opt-in enabled revoke: %v", err)
	}
	client.allowSecurityRuleRevocation = true
	for mode, want := range map[string]provider.FailureKind{"foreign": provider.Unauthorized, "default": provider.DeletionBlocked, "changed": provider.DeletionBlocked} {
		modes.Store(mode)
		if send, err := client.PrepareSecurityRuleRevocation(ctx, scope, target); send != nil || !provider.IsFailure(err, want) {
			t.Fatalf("preflight %s unexpectedly passed: %v", mode, err)
		}
	}
	modes.Store("present")
	send, err := client.PrepareSecurityRuleRevocation(ctx, scope, target)
	if err != nil || send == nil {
		t.Fatal(err)
	}
	if _, err := send(ctx); err == nil || writes.Load() != 1 {
		t.Fatalf("write was repeated or ambiguous failure was accepted: %d %v", writes.Load(), err)
	}
	for _, mode := range []string{"present", "disagree"} {
		modes.Store(mode)
		if result, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); err != nil || result != provider.ActionPending {
			t.Fatalf("%s proof is not terminal: %s %v", mode, result, err)
		}
	}
	modes.Store("paginated")
	if _, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("incomplete filtered collection confirmed absence: %v", err)
	}
	modes.Store("missing")
	if result, err := client.ObserveSecurityRuleRevocation(ctx, scope, target); err != nil || result != provider.ActionSucceeded {
		t.Fatalf("scoped absence not observed: %s %v", result, err)
	}
	modes.Store("ingress")
	target.Name = "ingress tcp 443-443 192.0.2.0/24"
	send, err = client.PrepareSecurityRuleRevocation(ctx, scope, target)
	if err != nil || send == nil {
		t.Fatal(err)
	}
	if id, err := send(ctx); err != nil || id != "req-ingress-revoke" || writes.Load() != 2 {
		t.Fatalf("ingress receipt=%q writes=%d error=%v", id, writes.Load(), err)
	}
}
