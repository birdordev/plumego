package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type volumeOrder struct {
	spec                     provider.VolumeSpec
	tagKey, marker, snapshot string
}

func volumeOrderMarker(scope provider.Scope, tagKey, token string) string {
	return sha([]byte(tagKey + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func (c *Client) prepareVolumeOrder(scope provider.Scope, order volumeOrder, token string) func(context.Context) (provider.LaunchReceipt, error) {
	order.marker = volumeOrderMarker(scope, order.tagKey, token)
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.orderTencentVolume(ctx, scope.Region, order, token)
		case provider.AlibabaECS:
			return c.orderAlibabaVolume(ctx, scope.Region, order, token)
		default:
			return c.orderHuaweiVolume(ctx, scope.Region, order)
		}
	}
}

func (c *Client) orderTencentVolume(ctx context.Context, region string, order volumeOrder, token string) (provider.LaunchReceipt, error) {
	spec := order.spec
	input := struct {
		Placement                   struct{ Zone string }
		SnapshotId                  string `json:",omitempty"`
		DiskName, DiskType          string
		DiskChargeType, ClientToken string
		DiskSize, DiskCount         int
		Tags                        []tag
		Encrypt                     string `json:",omitempty"`
	}{SnapshotId: order.snapshot, DiskName: spec.Name, DiskType: spec.DiskType, DiskSize: spec.SizeGiB, DiskCount: 1,
		DiskChargeType: "POSTPAID_BY_HOUR", ClientToken: token, Tags: []tag{{Key: order.tagKey, Value: order.marker}}}
	input.Placement.Zone = spec.Zone
	if spec.Encrypted {
		input.Encrypt = "ENCRYPT"
	}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		RequestId string
		DiskIdSet []string
	}
	if err := c.tencent(ctx, "cbs", region, "CreateDisks", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt(result.DiskIdSet, result.RequestId, "disk-")
}

func (c *Client) orderAlibabaVolume(ctx context.Context, region string, order volumeOrder, token string) (provider.LaunchReceipt, error) {
	spec := order.spec
	query := url.Values{"RegionId": {region}, "ZoneId": {spec.Zone}, "DiskName": {spec.Name},
		"DiskCategory": {spec.DiskType}, "Size": {strconv.Itoa(spec.SizeGiB)}, "ClientToken": {token},
		"Tag.1.Key": {order.tagKey}, "Tag.1.Value": {order.marker}}
	if order.snapshot != "" {
		query.Set("SnapshotId", order.snapshot)
		query.Set("DiskChargeType", "PostPaid")
	}
	if spec.Encrypted {
		query.Set("Encrypted", "true")
	}
	var result struct{ RequestId, Code, DiskId, OrderId string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "CreateDisk", "2014-05-26", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	if order.snapshot == "" && result.OrderId != "" {
		return provider.LaunchReceipt{RequestID: result.RequestId}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return creationReceipt([]string{result.DiskId}, result.RequestId, "d-")
}

func (c *Client) orderHuaweiVolume(ctx context.Context, region string, order volumeOrder) (provider.LaunchReceipt, error) {
	spec := order.spec
	input := struct {
		Volume struct {
			SnapshotID  string            `json:"snapshot_id,omitempty"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Zone        string            `json:"availability_zone"`
			Type        string            `json:"volume_type"`
			Size        int               `json:"size"`
			Metadata    map[string]string `json:"metadata,omitempty"`
			Multiattach *bool             `json:"multiattach,omitempty"`
			Count       int               `json:"count"`
		} `json:"volume"`
	}{}
	input.Volume.SnapshotID, input.Volume.Name, input.Volume.Description = order.snapshot, spec.Name, order.tagKey+":"+order.marker
	input.Volume.Zone, input.Volume.Type, input.Volume.Size, input.Volume.Count = spec.Zone, spec.DiskType, spec.SizeGiB, 1
	if order.snapshot == "" {
		shared := false
		input.Volume.Multiattach = &shared
		if spec.Encrypted {
			input.Volume.Metadata = map[string]string{"__system__encrypted": "1", "__system__cmkid": c.profile.HuaweiVolumeKMSKeyID}
		}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := huaweiEVS(region, "/v2.1/"+c.profile.ProjectID+"/cloudvolumes")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	// EVS 要求 36 字符的 UUID 格式标识。
	req.Header.Set("X-Client-Token", huaweiLaunchToken(order.marker))
	var result struct {
		OrderID   string   `json:"order_id"`
		JobID     string   `json:"job_id"`
		VolumeIDs []string `json:"volume_ids"`
	}
	if err := c.request(req, &result, http.StatusAccepted); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.OrderID != "" || len(result.VolumeIDs) != 1 || !huaweiResourceID.MatchString(result.VolumeIDs[0]) {
		return provider.LaunchReceipt{RequestID: result.JobID}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.LaunchReceipt{RequestID: result.JobID, ResourceID: result.VolumeIDs[0]}, nil
}
