package awsec2

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ApplyInstance(ctx context.Context, scope provider.Scope, id, action string) (string, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return "", &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Instance || !regionName.MatchString(scope.Region) || id == "" {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	ids := []string{id}
	options := func(o *ec2.Options) {
		o.Region = scope.Region
		o.Retryer = aws.NopRetryer{}
	}
	switch action {
	case "delete":
		if !c.allowInstanceDelete {
			return "", &provider.Failure{Kind: provider.DeletionDisabled}
		}
		if err := c.guardTermination(ctx, id, options); err != nil {
			return "", err
		}
		out, err := c.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids}, options)
		if err != nil {
			return "", translate(ctx, err)
		}
		request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
		return request, nil
	case "start":
		out, err := c.ec2.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: ids}, options)
		if err != nil {
			return "", translate(ctx, err)
		}
		request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
		return request, nil
	case "stop":
		out, err := c.ec2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: ids}, options)
		if err != nil {
			return "", translate(ctx, err)
		}
		request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
		return request, nil
	case "reboot":
		out, err := c.ec2.RebootInstances(ctx, &ec2.RebootInstancesInput{InstanceIds: ids}, options)
		if err != nil {
			return "", translate(ctx, err)
		}
		request, _ := middleware.GetRequestIDMetadata(out.ResultMetadata)
		return request, nil
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) guardTermination(ctx context.Context, id string, options func(*ec2.Options)) error {
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	out, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}}, options)
	if err != nil || len(out.Reservations) != 1 {
		return blocked
	}
	reservation := out.Reservations[0]
	if aws.ToString(reservation.OwnerId) != c.account || len(reservation.Instances) != 1 ||
		aws.ToString(reservation.Instances[0].InstanceId) != id {
		return blocked
	}
	instance := reservation.Instances[0]
	root := aws.ToString(instance.RootDeviceName)
	if instance.RootDeviceType != types.DeviceTypeEbs || root == "" {
		return blocked
	}
	rootConfirmed := false
	for _, mapping := range instance.BlockDeviceMappings {
		if mapping.Ebs == nil || aws.ToString(mapping.Ebs.VolumeId) == "" || mapping.Ebs.DeleteOnTermination == nil || *mapping.Ebs.DeleteOnTermination {
			return blocked
		}
		rootConfirmed = rootConfirmed || aws.ToString(mapping.DeviceName) == root
	}
	if !rootConfirmed {
		return blocked
	}
	return nil
}
