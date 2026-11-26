package provider

import (
	"context"
	"regexp"
)

type NetworkDeletionTarget struct {
	ID       string
	ParentID string
}

type NetworkDeleter interface {
	PrepareNetworkDeletion(context.Context, Scope, NetworkDeletionTarget) (func(context.Context) (string, error), error)
	ObserveNetworkDeletion(context.Context, Scope, NetworkDeletionTarget) (ActionResult, error)
}

var (
	publicGroupID   = regexp.MustCompile(`^sg-[a-z0-9]{1,60}$`)
	publicVPCID     = regexp.MustCompile(`^vpc-[a-z0-9]{1,60}$`)
	tencentSubnetID = regexp.MustCompile(`^subnet-[a-z0-9]{1,60}$`)
	alibabaSubnetID = regexp.MustCompile(`^vsw-[a-z0-9]{1,60}$`)
	huaweiNetworkID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
)

func (t NetworkDeletionTarget) Valid(name string, kind Kind) bool {
	var vpc, subnet *regexp.Regexp
	switch name {
	case TencentCVM:
		vpc, subnet = publicVPCID, tencentSubnetID
	case AlibabaECS:
		vpc, subnet = publicVPCID, alibabaSubnetID
	case HuaweiECS:
		vpc, subnet = huaweiNetworkID, huaweiNetworkID
	default:
		return false
	}
	switch kind {
	case SecurityGroup:
		switch name {
		case HuaweiECS:
			return t.ParentID == "" && huaweiNetworkID.MatchString(t.ID)
		case AlibabaECS:
			return publicGroupID.MatchString(t.ID) && publicVPCID.MatchString(t.ParentID)
		default:
			return t.ParentID == "" && publicGroupID.MatchString(t.ID)
		}
	case Network:
		return t.ParentID == "" && vpc.MatchString(t.ID)
	case Subnet:
		return t.ID != t.ParentID && subnet.MatchString(t.ID) && vpc.MatchString(t.ParentID)
	default:
		return false
	}
}
