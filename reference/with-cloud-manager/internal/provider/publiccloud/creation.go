package publiccloud

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const launchTag = "plumego-cmp-launch"

var (
	launchTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9]{16,64}$`)
	launchNamePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _.:-]{0,79}$`)
	huaweiLaunchName   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)
)

func (c *Client) creationScope(scope provider.Scope, spec provider.InstanceSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	err := &provider.Failure{Kind: provider.CreationBlocked}
	if scope.Kind != provider.Instance || !slices.Contains(c.profile.Regions, scope.Region) ||
		!launchTokenPattern.MatchString(token) || !launchNamePattern.MatchString(spec.Name) ||
		strings.TrimSpace(spec.Name) != spec.Name || !resourceID.MatchString(spec.ImageID) {
		return err
	}
	switch c.name {
	case provider.TencentCVM:
		if validLaunchID(spec.ImageID, "img-") && validLaunchID(spec.NetworkID, "subnet-") && validLaunchID(spec.SecurityGroup, "sg-") {
			return nil
		}
	case provider.AlibabaECS:
		if len(spec.Name) >= 2 && validLaunchID(spec.NetworkID, "vsw-") && validLaunchID(spec.SecurityGroup, "sg-") {
			return nil
		}
	case provider.HuaweiECS:
		if huaweiLaunchName.MatchString(spec.Name) && huaweiResourceID.MatchString(spec.ImageID) &&
			huaweiResourceID.MatchString(spec.NetworkID) && huaweiResourceID.MatchString(spec.SecurityGroup) {
			return nil
		}
	}
	return err
}

func validLaunchID(id, prefix string) bool {
	return len(id) > len(prefix) && strings.HasPrefix(id, prefix) && resourceID.MatchString(id)
}

func launchMarker(scope provider.Scope, token string) string {
	return sha([]byte(launchTag + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func matchingLaunchTag(tags []*tag, marker string) bool {
	return matchingTag(tags, launchTag, marker)
}

func matchingTag(tags []*tag, key, value string) bool {
	matches := 0
	for _, t := range tags {
		if t != nil && t.Key == key {
			if t.Value != value {
				return false
			}
			matches++
		}
	}
	return matches == 1
}

func (c *Client) PrepareInstanceCreation(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.creationScope(scope, spec, token); err != nil {
		return nil, err
	}
	if !slices.Contains(c.profile.InstanceCreateSizes, spec.Size) || spec.Size == "" {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	marker := launchMarker(scope, token)
	switch c.name {
	case provider.TencentCVM:
		subnet, err := c.tencentLaunchSubnet(ctx, scope.Region, spec.NetworkID)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createTencentInstance(ctx, scope.Region, spec, token, marker, subnet)
		}, nil
	case provider.HuaweiECS:
		return c.prepareHuaweiCreation(ctx, scope.Region, spec, token, marker)
	default:
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createAlibabaInstance(ctx, scope.Region, spec, token, marker)
		}, nil
	}
}

func (c *Client) CreateInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token string) (provider.LaunchReceipt, error) {
	send, err := c.PrepareInstanceCreation(ctx, scope, spec, token)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	return send(ctx)
}

func (c *Client) FindCreatedInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token, instanceID string) (provider.Object, error) {
	if err := c.creationScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	if instanceID != "" && c.checkInstanceScope(scope, instanceID) != nil {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	marker := launchMarker(scope, token)
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentCreation(ctx, scope.Region, spec, marker, instanceID)
	case provider.HuaweiECS:
		return c.findHuaweiCreation(ctx, scope.Region, spec, marker, provider.LaunchReceipt{ResourceID: instanceID})
	default:
		return c.findAlibabaCreation(ctx, scope.Region, spec, marker, instanceID)
	}
}

func creationReceipt(ids []string, request, prefix string) (provider.LaunchReceipt, error) {
	receipt := provider.LaunchReceipt{RequestID: request}
	if len(ids) != 1 || !validLaunchID(ids[0], prefix) {
		return receipt, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	receipt.ResourceID = ids[0]
	return receipt, nil
}
