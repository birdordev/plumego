package publiccloud

import (
	"context"
	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareLoadBalancerBackendDetach(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendTarget) (provider.LoadBalancerBackendPlan, error) {
	if !c.profile.AllowLoadBalancerBackendDetach {
		return provider.LoadBalancerBackendPlan{}, &provider.Failure{Kind: provider.Unsupported}
	}
	member, err := c.readLoadBalancerBackendMember(ctx, scope, target, "")
	if err != nil {
		return provider.LoadBalancerBackendPlan{}, err
	}
	if !member.present || !member.ready || member.weight != 0 {
		return provider.LoadBalancerBackendPlan{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return provider.LoadBalancerBackendPlan{BindingKey: member.bindingKey, Apply: member.detach}, nil
}

func (c *Client) ObserveLoadBalancerBackendDetach(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendTarget, bindingKey, receipt string) (provider.ActionResult, error) {
	if !provider.ValidLoadBalancerBackendKey(bindingKey) {
		return "", invalidLoadBalancerBackend()
	}
	member, err := c.readLoadBalancerBackendMember(ctx, scope, target, bindingKey)
	if err != nil {
		return "", err
	}
	if state, err := c.observeBackendMemberJob(ctx, scope.Region, target.Product, target.GroupID, receipt, alibabaMemberDetach); err != nil || state != provider.ActionSucceeded {
		return state, err
	}
	if member.ready && !member.present {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}
