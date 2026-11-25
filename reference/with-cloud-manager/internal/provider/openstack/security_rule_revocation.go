package openstack

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ruleRevocationParent(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (*url.URL, neutronNetworkObject, error) {
	if err := c.checkScope(scope); err != nil {
		return nil, neutronNetworkObject{}, err
	}
	if scope.Kind != provider.SecurityGroupRule || !novaUUID.MatchString(target.ID) || !novaUUID.MatchString(target.GroupID) {
		return nil, neutronNetworkObject{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	base, _, err := c.endpoint(scope)
	if err != nil {
		return nil, neutronNetworkObject{}, err
	}
	group, err := c.readNeutronObject(ctx, base.JoinPath("security-groups", target.GroupID), "security_group")
	if err != nil {
		return nil, neutronNetworkObject{}, err
	}
	if group.ID != target.GroupID || !c.ownsNeutronObject(group) {
		return nil, neutronNetworkObject{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return base, group, nil
}

func (c *Client) PrepareSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	if !c.allowSecurityRuleRevocation {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	base, group, err := c.ruleRevocationParent(ctx, scope, target)
	if err != nil {
		return nil, err
	}
	if group.Name == "" || strings.EqualFold(group.Name, "default") || (group.Shared != nil && *group.Shared) {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	endpoint := base.JoinPath("security-group-rules", target.ID)
	object, err := c.observeSecurityRule(ctx, endpoint, target.ID)
	if err != nil {
		return nil, err
	}
	if !target.Matches(object) {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		headers, err := c.request(ctx, http.MethodDelete, endpoint, nil, nil, "")
		return headers.Get("X-Openstack-Request-Id"), err
	}, nil
}

func (c *Client) ObserveSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.ActionResult, error) {
	base, group, err := c.ruleRevocationParent(ctx, scope, target)
	if err != nil {
		return provider.ActionPending, err
	}
	if group.Rules == nil {
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, rule := range group.Rules {
		if !novaUUID.MatchString(rule.ID) {
			return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if rule.ID == target.ID {
			return provider.ActionPending, nil
		}
	}
	object, err := c.observeSecurityRule(ctx, base.JoinPath("security-group-rules", target.ID), target.ID)
	if provider.IsFailure(err, provider.OutcomeUnknown) {
		return provider.ActionSucceeded, nil
	}
	if err == nil {
		if parent, ok := provider.SecurityRuleParent(object); !ok || parent != target.GroupID {
			return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	return provider.ActionPending, err
}
