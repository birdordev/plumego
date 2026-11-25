package openstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

const novaVolumeVersion = "compute 2.79"

type novaVolumeProof struct {
	attached, detached bool
	preserved          bool
	detachSafe         bool
}

func (c *Client) ApplyVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (string, error) {
	blocked := &provider.Failure{Kind: provider.VolumeBlocked}
	if !c.allowVolumeChanges || change.Device != "" {
		return "", blocked
	}
	compute, _, err := c.endpoint(scope)
	if err != nil {
		return "", err
	}
	proof, err := c.readNovaVolumeProof(ctx, scope, change, compute)
	if provider.IsFailure(err, provider.InvalidResponse) {
		return "", blocked
	}
	if err != nil {
		return "", err
	}
	attachments := compute.JoinPath("servers", change.InstanceID, "os-volume_attachments")
	if change.Attach {
		if proof.attached && proof.preserved {
			return "", nil
		}
		if !proof.detached {
			return "", blocked
		}
		payload, err := json.Marshal(map[string]any{"volumeAttachment": map[string]any{
			"volumeId": change.VolumeID, "delete_on_termination": false,
		}})
		if err != nil {
			return "", blocked
		}
		headers, err := c.requestStatus(ctx, http.MethodPost, attachments, payload, nil, novaVolumeVersion, http.StatusOK)
		return headers.Get("X-Openstack-Request-Id"), err
	}
	if proof.detached {
		return "", nil
	}
	if !proof.attached || !proof.detachSafe {
		return "", blocked
	}
	headers, err := c.requestStatus(ctx, http.MethodDelete, attachments.JoinPath(change.VolumeID), nil, nil, novaVolumeVersion, http.StatusAccepted)
	return headers.Get("X-Openstack-Request-Id"), err
}

func (c *Client) ObserveVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (bool, error) {
	compute, _, err := c.endpoint(scope)
	if err != nil {
		return false, err
	}
	proof, err := c.readNovaVolumeProof(ctx, scope, change, compute)
	if change.Attach {
		return proof.attached && proof.preserved, err
	}
	return proof.detached, err
}

type novaVolumeServer struct {
	ID      string          `json:"id"`
	Project string          `json:"tenant_id"`
	Status  string          `json:"status"`
	Image   json.RawMessage `json:"image"`
	Root    string          `json:"OS-EXT-SRV-ATTR:root_device_name"`
	Volumes []struct {
		ID                  string `json:"id"`
		DeleteOnTermination *bool  `json:"delete_on_termination"`
	} `json:"os-extended-volumes:volumes_attached"`
}

type cinderVolume struct {
	ID          string `json:"id"`
	Project     string `json:"os-vol-tenant-attr:tenant_id"`
	Status      string `json:"status"`
	Bootable    string `json:"bootable"`
	Multiattach *bool  `json:"multiattach"`
	Attachments []struct {
		InstanceID string `json:"server_id"`
		VolumeID   string `json:"volume_id"`
		Device     string `json:"device"`
	} `json:"attachments"`
}

func (c *Client) readNovaVolumeProof(ctx context.Context, scope provider.Scope, change provider.VolumeChange, compute *url.URL) (novaVolumeProof, error) {
	if err := c.checkScope(scope); err != nil {
		return novaVolumeProof{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if scope.Kind != provider.Instance || !novaUUID.MatchString(change.InstanceID) || !novaUUID.MatchString(change.VolumeID) {
		return novaVolumeProof{}, invalid
	}
	var response struct {
		Server novaVolumeServer `json:"server"`
	}
	if _, err := c.request(ctx, http.MethodGet, compute.JoinPath("servers", change.InstanceID), nil, &response, novaVolumeVersion); err != nil {
		return novaVolumeProof{}, err
	}
	server := response.Server
	_, project, _ := strings.Cut(c.identity, ":")
	if server.ID != change.InstanceID || server.Project != project || server.Volumes == nil || (server.Status != "ACTIVE" && server.Status != "SHUTOFF") {
		return novaVolumeProof{}, invalid
	}
	scope.Kind = provider.Volume
	cinder, _, err := c.endpoint(scope)
	if err != nil {
		return novaVolumeProof{}, err
	}
	var observed struct {
		Volume cinderVolume `json:"volume"`
	}
	if _, err := c.request(ctx, http.MethodGet, cinder.JoinPath("volumes", change.VolumeID), nil, &observed, ""); err != nil {
		return novaVolumeProof{}, err
	}
	volume := observed.Volume
	if volume.ID != change.VolumeID || (volume.Project != "" && volume.Project != project) || volume.Multiattach == nil ||
		*volume.Multiattach || volume.Attachments == nil || len(volume.Attachments) > 1 {
		return novaVolumeProof{}, invalid
	}
	return matchNovaVolume(server, volume, change)
}

func matchNovaVolume(server novaVolumeServer, volume cinderVolume, change provider.VolumeChange) (novaVolumeProof, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	var links int
	var preserved bool
	for _, disk := range server.Volumes {
		if disk.ID == change.VolumeID {
			links++
			preserved = disk.DeleteOnTermination != nil && !*disk.DeleteOnTermination
		}
	}
	if links > 1 {
		return novaVolumeProof{}, invalid
	}
	proof := novaVolumeProof{preserved: preserved, detached: links == 0 && len(volume.Attachments) == 0 && volume.Status == "available"}
	if len(volume.Attachments) == 0 {
		return proof, nil
	}
	attachment := volume.Attachments[0]
	if attachment.InstanceID != change.InstanceID || (attachment.VolumeID != "" && attachment.VolumeID != change.VolumeID) {
		return novaVolumeProof{}, invalid
	}
	proof.attached = links == 1 && volume.Status == "in-use"
	var image novaCreateRef
	imageBacked := json.Unmarshal(server.Image, &image) == nil && novaUUID.MatchString(image.ID)
	root := strings.TrimPrefix(server.Root, "/dev/")
	device := strings.TrimPrefix(attachment.Device, "/dev/")
	proof.detachSafe = preserved && volume.Bootable == "false" && device != "" && device != root && (imageBacked || root != "")
	return proof, nil
}
