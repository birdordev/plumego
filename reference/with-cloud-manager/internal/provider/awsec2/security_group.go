package awsec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createSecurityGroup(ctx context.Context, region string, spec provider.NetworkSpec, tags []types.Tag, options func(*ec2.Options)) (provider.LaunchReceipt, error) {
	if _, err := c.ownedVPC(ctx, region, spec.ParentID); err != nil {
		return provider.LaunchReceipt{}, err
	}
	out, err := c.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName: aws.String(spec.Name), VpcId: aws.String(spec.ParentID), Description: aws.String("Managed by Plumego"),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroup, Tags: tags}},
	}, options)
	if err != nil {
		return provider.LaunchReceipt{}, translate(ctx, err)
	}
	request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
	receipt := provider.LaunchReceipt{RequestID: request}
	if validResourceID(aws.ToString(out.GroupId), "sg-") {
		receipt.ResourceID = aws.ToString(out.GroupId)
	}
	return receipt, nil
}

func (c *Client) findCreatedSecurityGroup(ctx context.Context, spec provider.NetworkSpec, token, id string, filters []types.Filter, options func(*ec2.Options)) (provider.Object, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	input := &ec2.DescribeSecurityGroupsInput{Filters: filters, MaxResults: aws.Int32(10)}
	if id != "" {
		if !validResourceID(id, "sg-") {
			return provider.Object{}, invalid
		}
		input = &ec2.DescribeSecurityGroupsInput{GroupIds: []string{id}}
	}
	out, err := c.ec2.DescribeSecurityGroups(ctx, input, options)
	if err != nil {
		return provider.Object{}, translate(ctx, err)
	}
	if len(out.SecurityGroups) == 0 && aws.ToString(out.NextToken) == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if len(out.SecurityGroups) != 1 || aws.ToString(out.NextToken) != "" {
		return provider.Object{}, invalid
	}
	group := out.SecurityGroups[0]
	actual := aws.ToString(group.GroupId)
	if !validResourceID(actual, "sg-") || (id != "" && id != actual) || aws.ToString(group.OwnerId) != c.account ||
		aws.ToString(group.VpcId) != spec.ParentID || aws.ToString(group.GroupName) != spec.Name ||
		!matchesNetworkTag(group.Tags, token) || len(group.IpPermissions) != 0 {
		return provider.Object{}, invalid
	}
	result := taggedItem(group.GroupId, group.GroupName, "available", group.Tags)
	result.Link(provider.Network, spec.ParentID, "in_network")
	return result, nil
}
