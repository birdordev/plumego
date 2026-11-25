package provider

import (
	"context"
	"fmt"
	"net/netip"
)

type SecurityRuleTarget struct {
	ID, GroupID, Name string
}

func (t SecurityRuleTarget) Matches(object Object) bool {
	parent, ok := SecurityRuleParent(object)
	return ok && parent == t.GroupID && object.ID == t.ID && object.Name == t.Name
}

type SecurityRuleRevocation func(context.Context) (string, error)

type SecurityRuleRevoker interface {
	PrepareSecurityRuleRevocation(context.Context, Scope, SecurityRuleTarget) (SecurityRuleRevocation, error)
	ObserveSecurityRuleRevocation(context.Context, Scope, SecurityRuleTarget) (ActionResult, error)
}

type SecurityRule struct {
	Direction  string `json:"direction"`
	Protocol   string `json:"protocol"`
	FromPort   int32  `json:"from_port"`
	ToPort     int32  `json:"to_port"`
	RemoteCIDR string `json:"remote_cidr"`
}

func (r SecurityRule) Valid() bool {
	prefix, err := netip.ParsePrefix(r.RemoteCIDR)
	return err == nil && prefix.Addr().Is4() && prefix.Masked().String() == r.RemoteCIDR &&
		(r.Direction == "ingress" || r.Direction == "egress") && (r.Protocol == "tcp" || r.Protocol == "udp") &&
		r.FromPort >= 1 && r.ToPort >= r.FromPort && r.ToPort <= 65535
}

func (r SecurityRule) Asset(id, group string) Object {
	return ObservedSecurityRule(id, group, r.Direction, r.Protocol, "IPv4", &r.FromPort, &r.ToPort, r.RemoteCIDR)
}

func ObservedSecurityRule(id, group, direction, protocol, family string, from, to *int32, remote string) Object {
	if protocol == "" || protocol == "-1" {
		protocol = "any"
	}
	name := direction + " " + protocol
	if from != nil || to != nil {
		name += " " + ruleNumber(from) + "-" + ruleNumber(to)
	}
	if family != "" && family != "IPv4" {
		name += " " + family
	}
	if remote == "" {
		remote = "any"
	}
	object := Object{ID: id, Name: name + " " + remote, Status: "available"}
	object.Link(SecurityGroup, group, "in_security_group")
	return object
}

func ruleNumber(value *int32) string {
	if value == nil {
		return "any"
	}
	return fmt.Sprint(*value)
}

func SecurityRuleParent(object Object) (string, bool) {
	if len(object.Links) != 1 {
		return "", false
	}
	parent := object.Links[0]
	return parent.ID, parent.Kind == SecurityGroup && parent.Relation == "in_security_group" && parent.ID != ""
}
