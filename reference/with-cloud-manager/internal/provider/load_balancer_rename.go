package provider

import (
	"context"
	"regexp"
)

type LoadBalancerRename struct {
	ID, Product, CurrentName, Name string
	ListenerID, TargetGroupID      string
}

type LoadBalancerRenamer interface {
	PrepareLoadBalancerRename(ctx context.Context, scope Scope, target LoadBalancerRename) (func(context.Context) (string, error), error)
	ObserveLoadBalancerRename(ctx context.Context, scope Scope, target LoadBalancerRename, receipt string) (ActionResult, error)
}

var loadBalancerRenameName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,59}$`)

func ValidLoadBalancerRenameName(name string) bool {
	return loadBalancerRenameName.MatchString(name)
}

func (target LoadBalancerRename) ValidFor(vendor string) bool {
	if !ValidLoadBalancerRenameName(target.Name) || target.CurrentName == target.Name ||
		!validLoadBalancerComponentText(target.CurrentName, 1024) || !validLoadBalancerComponentID(target.ID) {
		return false
	}
	if target.ListenerID != "" && (!ValidLoadBalancerListenerID(target.ListenerID) || target.Product == "alibaba-clb") {
		return false
	}
	if target.TargetGroupID != "" && (target.ListenerID != "" || !ValidLoadBalancerTargetGroupID(target.TargetGroupID) || target.Product == "alibaba-clb") {
		return false
	}
	return validLoadBalancerProduct(vendor, target.Product)
}

func validLoadBalancerProduct(vendor, product string) bool {
	switch vendor {
	case TencentCVM:
		return product == "tencent-clb"
	case AlibabaECS:
		return product == "alibaba-alb" || product == "alibaba-nlb" || product == "alibaba-clb"
	case HuaweiECS:
		return product == "huawei-elb"
	default:
		return false
	}
}
