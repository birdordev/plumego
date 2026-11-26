package provider

import "context"

type LoadBalancerBackendAttach struct {
	ParentID, Product, GroupID, InstanceID string
	Port                                   int
	Secret                                 []byte `json:"-"`
}

func (target LoadBalancerBackendAttach) ValidFor(vendor string) bool {
	return validLoadBalancerProduct(vendor, target.Product) && target.Product != "alibaba-clb" &&
		validLoadBalancerComponentID(target.ParentID) && ValidLoadBalancerTargetGroupID(target.GroupID) &&
		validLoadBalancerComponentID(target.InstanceID) && target.Port > 0 && target.Port <= 65535 && len(target.Secret) == 32
}

type LoadBalancerBackendAttached struct {
	Status    ActionResult
	MemberKey string
}

type LoadBalancerBackendAttacher interface {
	PrepareLoadBalancerBackendAttach(context.Context, Scope, LoadBalancerBackendAttach) (LoadBalancerBackendPlan, error)
	ObserveLoadBalancerBackendAttach(ctx context.Context, scope Scope, target LoadBalancerBackendAttach, bindingKey, receipt string) (LoadBalancerBackendAttached, error)
}
