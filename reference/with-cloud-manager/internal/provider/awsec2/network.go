package awsec2

import (
	"context"
	"net/netip"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"with-cloud-manager/internal/provider"
)

const networkIntentTag = "plumego-network-intent"

func (c *Client) networkScope(scope provider.Scope, spec provider.NetworkSpec, token string) (netip.Prefix, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return netip.Prefix{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	blocked := &provider.Failure{Kind: provider.CreationBlocked}
	if !regionName.MatchString(scope.Region) || !ec2LaunchToken.MatchString(token) {
		return netip.Prefix{}, blocked
	}
	if scope.Kind == provider.SecurityGroupRule {
		if spec.Rule == nil || !spec.Rule.Valid() || spec.Name != "" || spec.CIDR != "" || !validResourceID(spec.ParentID, "sg-") {
			return netip.Prefix{}, blocked
		}
		return netip.Prefix{}, nil
	}
	if spec.Rule != nil {
		return netip.Prefix{}, blocked
	}
	if scope.Kind == provider.SecurityGroup {
		name := strings.ToLower(spec.Name)
		if name == "" || name == "default" || strings.HasPrefix(name, "sg-") ||
			spec.CIDR != "" || !validResourceID(spec.ParentID, "vpc-") {
			return netip.Prefix{}, blocked
		}
		return netip.Prefix{}, nil
	}
	prefix, err := netip.ParsePrefix(spec.CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.Bits() < 16 || prefix.Bits() > 28 ||
		(scope.Kind != provider.Network && scope.Kind != provider.Subnet) ||
		(scope.Kind == provider.Network && spec.ParentID != "") ||
		(scope.Kind == provider.Subnet && !validResourceID(spec.ParentID, "vpc-")) {
		return netip.Prefix{}, blocked
	}
	return prefix, nil
}

func (c *Client) CreateNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	prefix, err := c.networkScope(scope, spec, token)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.createSecurityRule(ctx, scope.Region, spec, token)
	}
	allowed := c.allowNetworkCreation
	if scope.Kind == provider.SecurityGroup {
		allowed = c.allowSecurityGroupCreation
	}
	if !allowed {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	sendOnce := func(o *ec2.Options) {
		o.Region = scope.Region
		o.Retryer = aws.NopRetryer{}
	}
	tags := []types.Tag{{Key: aws.String("Name"), Value: aws.String(spec.Name)},
		{Key: aws.String(networkIntentTag), Value: aws.String(token)}}
	if scope.Kind == provider.SecurityGroup {
		return c.createSecurityGroup(ctx, scope.Region, spec, tags, sendOnce)
	}
	if scope.Kind == provider.Network {
		out, err := c.ec2.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String(spec.CIDR),
			TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeVpc, Tags: tags}}}, sendOnce)
		if err != nil {
			return provider.LaunchReceipt{}, translate(ctx, err)
		}
		request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
		receipt := provider.LaunchReceipt{RequestID: request}
		if out.Vpc != nil && validResourceID(aws.ToString(out.Vpc.VpcId), "vpc-") {
			receipt.ResourceID = aws.ToString(out.Vpc.VpcId)
		}
		return receipt, nil
	}
	if err := c.checkSubnetParent(ctx, scope.Region, spec.ParentID, prefix); err != nil {
		return provider.LaunchReceipt{}, err
	}
	out, err := c.ec2.CreateSubnet(ctx, &ec2.CreateSubnetInput{VpcId: aws.String(spec.ParentID), CidrBlock: aws.String(spec.CIDR),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSubnet, Tags: tags}}}, sendOnce)
	if err != nil {
		return provider.LaunchReceipt{}, translate(ctx, err)
	}
	request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
	receipt := provider.LaunchReceipt{RequestID: request}
	if out.Subnet != nil && validResourceID(aws.ToString(out.Subnet.SubnetId), "subnet-") {
		receipt.ResourceID = aws.ToString(out.Subnet.SubnetId)
	}
	return receipt, nil
}

func (c *Client) ownedVPC(ctx context.Context, region, id string) (types.Vpc, error) {
	blocked := &provider.Failure{Kind: provider.CreationBlocked}
	out, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{id}}, func(o *ec2.Options) { o.Region = region })
	if err != nil || len(out.Vpcs) != 1 || aws.ToString(out.NextToken) != "" {
		return types.Vpc{}, blocked
	}
	vpc := out.Vpcs[0]
	if aws.ToString(vpc.VpcId) != id || aws.ToString(vpc.OwnerId) != c.account || vpc.State != types.VpcStateAvailable {
		return types.Vpc{}, blocked
	}
	return vpc, nil
}

func (c *Client) checkSubnetParent(ctx context.Context, region, id string, subnet netip.Prefix) error {
	vpc, err := c.ownedVPC(ctx, region, id)
	if err != nil {
		return err
	}
	contains := func(raw *string) bool {
		prefix, err := netip.ParsePrefix(aws.ToString(raw))
		return err == nil && prefix.Bits() <= subnet.Bits() && prefix.Contains(subnet.Addr())
	}
	if contains(vpc.CidrBlock) {
		return nil
	}
	for _, association := range vpc.CidrBlockAssociationSet {
		if association.CidrBlockState != nil && association.CidrBlockState.State == types.VpcCidrBlockStateCodeAssociated && contains(association.CidrBlock) {
			return nil
		}
	}
	return &provider.Failure{Kind: provider.CreationBlocked}
}

func (c *Client) FindCreatedNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token, id string) (provider.Object, error) {
	if _, err := c.networkScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	opts := func(o *ec2.Options) { o.Region = scope.Region }
	filters := []types.Filter{{Name: aws.String("tag:" + networkIntentTag), Values: []string{token}}}
	if scope.Kind == provider.SecurityGroupRule {
		return c.findCreatedSecurityRule(ctx, spec, token, id, filters, opts)
	}
	if scope.Kind == provider.SecurityGroup {
		return c.findCreatedSecurityGroup(ctx, spec, token, id, filters, opts)
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	missing := &provider.Failure{Kind: provider.OutcomeUnknown}
	var actual, owner, cidr, state, parent, prefix string
	var tags []types.Tag
	if scope.Kind == provider.Network {
		prefix = "vpc-"
		input := &ec2.DescribeVpcsInput{Filters: filters, MaxResults: aws.Int32(10)}
		if id != "" {
			if !validResourceID(id, prefix) {
				return provider.Object{}, invalid
			}
			input = &ec2.DescribeVpcsInput{VpcIds: []string{id}}
		}
		out, err := c.ec2.DescribeVpcs(ctx, input, opts)
		if err != nil {
			return provider.Object{}, translate(ctx, err)
		}
		if len(out.Vpcs) == 0 && aws.ToString(out.NextToken) == "" {
			return provider.Object{}, missing
		}
		if len(out.Vpcs) != 1 || aws.ToString(out.NextToken) != "" {
			return provider.Object{}, invalid
		}
		row := out.Vpcs[0]
		actual, owner, cidr, state, tags = aws.ToString(row.VpcId), aws.ToString(row.OwnerId), aws.ToString(row.CidrBlock), string(row.State), row.Tags
	} else {
		prefix = "subnet-"
		input := &ec2.DescribeSubnetsInput{Filters: filters, MaxResults: aws.Int32(10)}
		if id != "" {
			if !validResourceID(id, prefix) {
				return provider.Object{}, invalid
			}
			input = &ec2.DescribeSubnetsInput{SubnetIds: []string{id}}
		}
		out, err := c.ec2.DescribeSubnets(ctx, input, opts)
		if err != nil {
			return provider.Object{}, translate(ctx, err)
		}
		if len(out.Subnets) == 0 && aws.ToString(out.NextToken) == "" {
			return provider.Object{}, missing
		}
		if len(out.Subnets) != 1 || aws.ToString(out.NextToken) != "" {
			return provider.Object{}, invalid
		}
		row := out.Subnets[0]
		actual, owner, cidr, state, tags = aws.ToString(row.SubnetId), aws.ToString(row.OwnerId), aws.ToString(row.CidrBlock), string(row.State), row.Tags
		parent = aws.ToString(row.VpcId)
	}
	if !validResourceID(actual, prefix) || (id != "" && actual != id) || owner != c.account ||
		cidr != spec.CIDR || parent != spec.ParentID || aws.ToString(tagName(tags)) != spec.Name ||
		!matchesNetworkTag(tags, token) {
		return provider.Object{}, invalid
	}
	object := taggedItem(&actual, tagName(tags), state, tags)
	object.Link(provider.Network, parent, "in_network")
	return object, nil
}

func matchesNetworkTag(tags []types.Tag, token string) bool {
	matches := 0
	for _, tag := range tags {
		if aws.ToString(tag.Key) == networkIntentTag {
			if aws.ToString(tag.Value) != token {
				return false
			}
			matches++
		}
	}
	return matches == 1
}
