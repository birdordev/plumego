package awsec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"with-cloud-manager/internal/provider"
)

func (c *Client) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	page, err := c.fetch(ctx, scope, cursor, "")
	if err != nil {
		return provider.Page{}, err
	}
	return page, page.Validate(cursor)
}

func (c *Client) Observe(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if id == "" || len(id) > provider.MaxObjectIDSize || scope.Kind == provider.Region {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	page, err := c.fetch(ctx, scope, "", id)
	if err != nil {
		return provider.Object{}, err
	}
	if len(page.Objects) != 1 || page.Objects[0].ID != id {
		if scope.Kind == provider.SecurityGroupRule && len(page.Objects) != 0 {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return page.Objects[0], page.Validate("")
}

func (c *Client) fetch(ctx context.Context, scope provider.Scope, cursor, id string) (provider.Page, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	region := scope.Region
	if scope.Kind == provider.Region && region == "" {
		region = c.region
	} else if !regionName.MatchString(region) || scope.Kind == provider.Region {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	opts := func(o *ec2.Options) { o.Region = region }
	var ids []string
	var limit *int32
	var next *string
	if id != "" {
		ids = []string{id}
	} else {
		limit = aws.Int32(100)
		if cursor != "" {
			next = aws.String(cursor)
		}
	}
	var result provider.Page
	switch scope.Kind {
	case provider.Region:
		if cursor != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		out, err := c.ec2.DescribeRegions(ctx, &ec2.DescribeRegionsInput{}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		for _, row := range out.Regions {
			result.Objects = append(result.Objects, item(row.RegionName, row.RegionName, aws.ToString(row.OptInStatus)))
		}
	case provider.Zone:
		if cursor != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		out, err := c.ec2.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{ZoneIds: ids}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		for _, row := range out.AvailabilityZones {
			result.Objects = append(result.Objects, item(row.ZoneId, row.ZoneName, string(row.State)))
		}
	case provider.Instance:
		out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids, MaxResults: limit, NextToken: next}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, reservation := range out.Reservations {
			if (id != "" || reservation.OwnerId != nil) && aws.ToString(reservation.OwnerId) != c.account {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			for _, row := range reservation.Instances {
				result.Objects = append(result.Objects, instanceObject(row))
			}
		}
	case provider.Volume:
		out, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: ids, MaxResults: limit, NextToken: next}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, row := range out.Volumes {
			asset := taggedItem(row.VolumeId, tagName(row.Tags), string(row.State), row.Tags)
			for _, attachment := range row.Attachments {
				asset.Link(provider.Instance, aws.ToString(attachment.InstanceId), "attached_to")
			}
			result.Objects = append(result.Objects, asset)
		}
	case provider.Network:
		out, err := c.ec2.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: ids, MaxResults: limit, NextToken: next}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, row := range out.Vpcs {
			result.Objects = append(result.Objects, taggedItem(row.VpcId, tagName(row.Tags), string(row.State), row.Tags))
		}
	case provider.Subnet:
		out, err := c.ec2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: ids, MaxResults: limit, NextToken: next}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, row := range out.Subnets {
			asset := taggedItem(row.SubnetId, tagName(row.Tags), string(row.State), row.Tags)
			asset.Link(provider.Network, aws.ToString(row.VpcId), "in_network")
			result.Objects = append(result.Objects, asset)
		}
	case provider.SecurityGroup:
		out, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: ids, MaxResults: limit, NextToken: next}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, row := range out.SecurityGroups {
			asset := taggedItem(row.GroupId, row.GroupName, "", row.Tags)
			asset.Link(provider.Network, aws.ToString(row.VpcId), "in_network")
			result.Objects = append(result.Objects, asset)
		}
	case provider.SecurityGroupRule:
		if id != "" && !validResourceID(id, "sgr-") {
			return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
		}
		out, err := c.ec2.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
			SecurityGroupRuleIds: ids, MaxResults: limit, NextToken: next,
		}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		if id != "" && result.Next != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range out.SecurityGroupRules {
			asset, err := c.inventorySecurityRule(row)
			if err != nil {
				return provider.Page{}, err
			}
			result.Objects = append(result.Objects, asset)
		}
	case provider.Image:
		out, err := c.ec2.DescribeImages(ctx, &ec2.DescribeImagesInput{
			ImageIds: ids, Owners: []string{"self"}, MaxResults: limit, NextToken: next,
		}, opts)
		if err != nil {
			return provider.Page{}, translate(ctx, err)
		}
		result.Next = aws.ToString(out.NextToken)
		for _, row := range out.Images {
			result.Objects = append(result.Objects, taggedItem(row.ImageId, row.Name, string(row.State), row.Tags))
		}
	default:
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	return result, nil
}

func instanceObject(row types.Instance) provider.Object {
	var state string
	if row.State != nil {
		state = string(row.State.Name)
	}
	asset := taggedItem(row.InstanceId, tagName(row.Tags), state, row.Tags)
	asset.Link(provider.Network, aws.ToString(row.VpcId), "in_network")
	asset.Link(provider.Subnet, aws.ToString(row.SubnetId), "in_subnet")
	asset.Link(provider.Image, aws.ToString(row.ImageId), "uses_image")
	if row.Placement != nil {
		asset.Link(provider.Zone, aws.ToString(row.Placement.AvailabilityZoneId), "in_zone")
	}
	for _, group := range row.SecurityGroups {
		asset.Link(provider.SecurityGroup, aws.ToString(group.GroupId), "secured_by")
	}
	for _, mapping := range row.BlockDeviceMappings {
		if mapping.Ebs != nil {
			asset.Link(provider.Volume, aws.ToString(mapping.Ebs.VolumeId), "uses_volume")
		}
	}
	return asset
}

func item(id, name *string, state string) provider.Object {
	return provider.Object{ID: aws.ToString(id), Name: aws.ToString(name), Status: state}
}

func tagName(tags []types.Tag) *string {
	for _, tag := range tags {
		if aws.ToString(tag.Key) == "Name" {
			return tag.Value
		}
	}
	return nil
}

func taggedItem(id, name *string, state string, tags []types.Tag) provider.Object {
	asset := item(id, name, state)
	asset.Tags = make(map[string]string, len(tags))
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil && *tag.Key != networkIntentTag {
			asset.Tags[*tag.Key] = *tag.Value
		}
	}
	return asset
}
