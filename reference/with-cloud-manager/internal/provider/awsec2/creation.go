package awsec2

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"with-cloud-manager/internal/provider"
)

var awsResourceID = regexp.MustCompile(`^[a-z]+-[0-9a-f]{8,32}$`)
var ec2LaunchToken = regexp.MustCompile(`^[a-zA-Z0-9]{16,64}$`)

func (c *Client) creationScope(scope provider.Scope, spec provider.InstanceSpec, token string) error {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Instance || !regionName.MatchString(scope.Region) || !ec2LaunchToken.MatchString(token) ||
		!validResourceID(spec.ImageID, "ami-") || !validResourceID(spec.NetworkID, "subnet-") || !validResourceID(spec.SecurityGroup, "sg-") {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func validResourceID(id, prefix string) bool {
	return strings.HasPrefix(id, prefix) && awsResourceID.MatchString(id)
}

func (c *Client) CreateInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token string) (provider.LaunchReceipt, error) {
	if err := c.creationScope(scope, spec, token); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if !slices.Contains(c.instanceCreateSizes, spec.Size) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	out, err := c.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String(spec.ImageID), InstanceType: types.InstanceType(spec.Size), MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
		ClientToken: aws.String(token),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex: aws.Int32(0), SubnetId: aws.String(spec.NetworkID), Groups: []string{spec.SecurityGroup},
			AssociatePublicIpAddress: aws.Bool(false),
		}},
		MetadataOptions: &types.InstanceMetadataOptionsRequest{HttpTokens: types.HttpTokensStateRequired},
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeInstance,
			Tags: []types.Tag{{Key: aws.String("Name"), Value: aws.String(spec.Name)}}}},
	}, func(o *ec2.Options) {
		o.Region = scope.Region
		o.Retryer = aws.NopRetryer{}
	})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) {
			switch api.ErrorCode() {
			case "UnauthorizedOperation", "InvalidAMIID.NotFound", "InvalidSubnetID.NotFound", "InvalidGroup.NotFound",
				"InvalidParameterValue", "InvalidParameterCombination", "InsufficientInstanceCapacity", "InstanceLimitExceeded":
				return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
			}
		}
		return provider.LaunchReceipt{}, translate(ctx, err)
	}
	request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
	receipt := provider.LaunchReceipt{RequestID: request}
	if len(out.Instances) == 1 && validResourceID(aws.ToString(out.Instances[0].InstanceId), "i-") {
		receipt.ResourceID = aws.ToString(out.Instances[0].InstanceId)
	}
	return receipt, nil
}

func (c *Client) FindCreatedInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token, instanceID string) (provider.Object, error) {
	if err := c.creationScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{{Name: aws.String("client-token"), Values: []string{token}}}, MaxResults: aws.Int32(10),
	}, func(o *ec2.Options) { o.Region = scope.Region })
	if err != nil {
		return provider.Object{}, translate(ctx, err)
	}
	if len(out.Reservations) == 0 && aws.ToString(out.NextToken) == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if len(out.Reservations) != 1 || aws.ToString(out.NextToken) != "" {
		return provider.Object{}, invalid
	}
	reservation := out.Reservations[0]
	if aws.ToString(reservation.OwnerId) != c.account || len(reservation.Instances) != 1 {
		return provider.Object{}, invalid
	}
	row := reservation.Instances[0]
	id := aws.ToString(row.InstanceId)
	if row.State == nil || !validResourceID(id, "i-") || (instanceID != "" && id != instanceID) || aws.ToString(row.ClientToken) != token ||
		aws.ToString(row.ImageId) != spec.ImageID || aws.ToString(row.SubnetId) != spec.NetworkID || string(row.InstanceType) != spec.Size ||
		len(row.SecurityGroups) != 1 || aws.ToString(row.SecurityGroups[0].GroupId) != spec.SecurityGroup {
		return provider.Object{}, invalid
	}
	return instanceObject(row), nil
}
