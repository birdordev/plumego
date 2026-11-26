package publiccloud

import (
	"context"
	"net/http"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) readHuaweiGroupRule(ctx context.Context, region string, target provider.SecurityRuleTarget) (huaweiRule, error) {
	group, err := c.readHuaweiGroup(ctx, region, target.GroupID)
	if err != nil {
		return huaweiRule{}, err
	}
	if strings.EqualFold(group.Name, "default") {
		return huaweiRule{}, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	var match huaweiRule
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	seen := make(map[string]struct{}, len(group.Rules))
	for _, row := range group.Rules {
		if row.ProjectID != c.profile.ProjectID || row.GroupID != target.GroupID ||
			!huaweiResourceID.MatchString(row.ID) || !validRuleDirection(row.Direction) {
			return huaweiRule{}, invalid
		}
		if _, repeated := seen[row.ID]; repeated {
			return huaweiRule{}, invalid
		}
		seen[row.ID] = struct{}{}
		if row.ID == target.ID {
			match = row
		}
	}
	return match, nil
}

func (c *Client) prepareHuaweiRuleRevocation(ctx context.Context, region string, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	groupRule, err := c.readHuaweiGroupRule(ctx, region, target)
	if err != nil {
		return nil, err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	groupObject := groupRule.asset()
	if !target.Matches(groupObject) || groupRule.Enabled == nil {
		return nil, blocked
	}
	rule, err := c.readHuaweiRule(ctx, region, target.GroupID, target.ID)
	if err != nil {
		return nil, err
	}
	object := rule.asset()
	if !target.Matches(object) || rule.Enabled == nil || groupObject.Status != object.Status || groupRule.Description != rule.Description {
		return nil, blocked
	}
	endpoint := c.huaweiRuleEndpoint(region, target.ID)
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusNoContent)
	}, nil
}

func (c *Client) huaweiRulePresent(ctx context.Context, region string, target provider.SecurityRuleTarget) (bool, error) {
	row, err := c.readHuaweiGroupRule(ctx, region, target)
	if err != nil || row.ID != "" {
		return row.ID != "", err
	}
	_, err = c.readHuaweiRule(ctx, region, target.GroupID, target.ID)
	if provider.IsFailure(err, provider.OutcomeUnknown) {
		return false, nil
	}
	return err == nil, err
}
