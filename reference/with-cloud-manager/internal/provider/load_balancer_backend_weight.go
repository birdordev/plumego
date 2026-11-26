package provider

import "context"

type LoadBalancerBackendTarget struct {
	ParentID, Product, GroupID, MemberKey string
	Secret                                []byte `json:"-"`
}

func (target LoadBalancerBackendTarget) ValidFor(vendor string) bool {
	return validLoadBalancerProduct(vendor, target.Product) && target.Product != "alibaba-clb" &&
		validLoadBalancerComponentID(target.ParentID) && ValidLoadBalancerTargetGroupID(target.GroupID) &&
		ValidLoadBalancerBackendKey(target.MemberKey) && len(target.Secret) == 32
}

type LoadBalancerBackendWeight struct {
	LoadBalancerBackendTarget
	Current, Desired int
}

func (target LoadBalancerBackendWeight) ValidFor(vendor string) bool {
	return target.LoadBalancerBackendTarget.ValidFor(vendor) && target.Current >= 0 && target.Current <= 100 &&
		target.Desired >= 0 && target.Desired <= 100 && target.Current != target.Desired
}

type LoadBalancerBackendWeightOperator interface {
	PrepareLoadBalancerBackendWeight(context.Context, Scope, LoadBalancerBackendWeight) (func(context.Context) (string, error), error)
	ObserveLoadBalancerBackendWeight(context.Context, Scope, LoadBalancerBackendWeight, string) (ActionResult, error)
}

type LoadBalancerBackendPlan struct {
	BindingKey string
	Apply      func(context.Context) (string, error)
}

type LoadBalancerBackendDetacher interface {
	PrepareLoadBalancerBackendDetach(context.Context, Scope, LoadBalancerBackendTarget) (LoadBalancerBackendPlan, error)
	ObserveLoadBalancerBackendDetach(ctx context.Context, scope Scope, target LoadBalancerBackendTarget, bindingKey, receipt string) (ActionResult, error)
}
