package awsec2

import (
	"context"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"with-cloud-manager/internal/provider"
)

var dataDevice = regexp.MustCompile(`^/dev/sd[f-p]$`)

type ebsProof struct {
	attached, detached bool
	occupied, root     bool
	device             string
}

func (c *Client) ApplyVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (string, error) {
	blocked := &provider.Failure{Kind: provider.VolumeBlocked}
	if !c.allowVolumeChanges || (change.Attach && !dataDevice.MatchString(change.Device)) {
		return "", blocked
	}
	proof, err := c.readVolumeProof(ctx, scope, change)
	if provider.IsFailure(err, provider.InvalidResponse) {
		return "", blocked
	}
	if err != nil {
		return "", err
	}
	options := func(o *ec2.Options) { o.Region, o.Retryer = scope.Region, aws.NopRetryer{} }
	if change.Attach {
		if proof.attached && proof.device == change.Device {
			return "", nil
		}
		if !proof.detached || proof.occupied || proof.root {
			return "", blocked
		}
		result, err := c.ec2.AttachVolume(ctx, &ec2.AttachVolumeInput{
			InstanceId: aws.String(change.InstanceID), VolumeId: aws.String(change.VolumeID), Device: aws.String(change.Device),
		}, options)
		if err != nil {
			return "", translate(ctx, err)
		}
		id, _ := middleware.GetRequestIDMetadata(result.ResultMetadata)
		return id, nil
	}
	if proof.detached {
		return "", nil
	}
	if !proof.attached || proof.root || proof.device == "" {
		return "", blocked
	}
	result, err := c.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{
		InstanceId: aws.String(change.InstanceID), VolumeId: aws.String(change.VolumeID), Device: aws.String(proof.device), Force: aws.Bool(false),
	}, options)
	if err != nil {
		return "", translate(ctx, err)
	}
	id, _ := middleware.GetRequestIDMetadata(result.ResultMetadata)
	return id, nil
}

func (c *Client) ObserveVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (bool, error) {
	proof, err := c.readVolumeProof(ctx, scope, change)
	if err != nil {
		return false, err
	}
	if change.Attach {
		return proof.attached && proof.device == change.Device, nil
	}
	return proof.detached, nil
}

func (c *Client) readVolumeProof(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (ebsProof, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return ebsProof{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Instance || !regionName.MatchString(scope.Region) ||
		!validResourceID(change.InstanceID, "i-") || !validResourceID(change.VolumeID, "vol-") {
		return ebsProof{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	opts := func(o *ec2.Options) { o.Region = scope.Region }
	instanceResult, err := c.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{change.InstanceID}}, opts)
	if err != nil {
		return ebsProof{}, translate(ctx, err)
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if len(instanceResult.Reservations) != 1 || len(instanceResult.Reservations[0].Instances) != 1 ||
		aws.ToString(instanceResult.Reservations[0].OwnerId) != c.account {
		return ebsProof{}, invalid
	}
	instance := instanceResult.Reservations[0].Instances[0]
	if aws.ToString(instance.InstanceId) != change.InstanceID || instance.Placement == nil || aws.ToString(instance.RootDeviceName) == "" ||
		instance.State == nil || (instance.State.Name != types.InstanceStateNameRunning && instance.State.Name != types.InstanceStateNameStopped) {
		return ebsProof{}, invalid
	}
	volumes, err := c.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{change.VolumeID}}, opts)
	if err != nil {
		return ebsProof{}, translate(ctx, err)
	}
	if len(volumes.Volumes) != 1 || aws.ToString(volumes.Volumes[0].VolumeId) != change.VolumeID {
		return ebsProof{}, invalid
	}
	return matchEBS(instance, volumes.Volumes[0], change)
}

func matchEBS(instance types.Instance, volume types.Volume, change provider.VolumeChange) (ebsProof, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if aws.ToBool(volume.MultiAttachEnabled) || len(volume.Attachments) > 1 || aws.ToString(volume.AvailabilityZone) == "" ||
		aws.ToString(volume.AvailabilityZone) != aws.ToString(instance.Placement.AvailabilityZone) {
		return ebsProof{}, invalid
	}
	root := aws.ToString(instance.RootDeviceName)
	proof := ebsProof{root: change.Device != "" && sameEBSDevice(root, change.Device)}
	var instanceState types.AttachmentStatus
	for _, device := range instance.BlockDeviceMappings {
		name := aws.ToString(device.DeviceName)
		if device.Ebs != nil && aws.ToString(device.Ebs.VolumeId) == change.VolumeID {
			if proof.device != "" || name == "" {
				return ebsProof{}, invalid
			}
			proof.device, instanceState = name, device.Ebs.Status
			proof.root = proof.root || sameEBSDevice(name, root)
		} else if change.Device != "" && name != "" && sameEBSDevice(name, change.Device) {
			proof.occupied = true
		}
	}
	proof.detached = volume.State == types.VolumeStateAvailable && len(volume.Attachments) == 0 && proof.device == ""
	if len(volume.Attachments) == 1 {
		attachment := volume.Attachments[0]
		if aws.ToString(attachment.InstanceId) != change.InstanceID || aws.ToString(attachment.VolumeId) != change.VolumeID {
			return ebsProof{}, invalid
		}
		proof.attached = proof.device != "" && aws.ToString(attachment.Device) == proof.device && volume.State == types.VolumeStateInUse &&
			instanceState == types.AttachmentStatusAttached && attachment.State == types.VolumeAttachmentStateAttached
	}
	return proof, nil
}

func sameEBSDevice(first, second string) bool {
	normalize := func(name string) string {
		name = strings.TrimPrefix(name, "/dev/")
		name = strings.TrimPrefix(strings.TrimPrefix(name, "xvd"), "sd")
		return strings.TrimRight(name, "0123456789")
	}
	return normalize(first) == normalize(second)
}
