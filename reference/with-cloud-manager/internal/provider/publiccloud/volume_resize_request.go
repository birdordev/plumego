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

func (c *Client) resizeTencentDisk(ctx context.Context, region string, change provider.VolumeResize) (string, error) {
	body, err := json.Marshal(struct {
		DiskId   string
		DiskSize int
	}{change.ID, change.TargetGiB})
	if err != nil {
		return "", err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "cbs", region, "ResizeDisk", "2017-03-12", body, &response); err != nil {
		return "", err
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) resizeAlibabaDisk(ctx context.Context, region string, change provider.VolumeResize) (string, error) {
	query := url.Values{"DiskId": {change.ID}, "Type": {"offline"}, "NewSize": {strconv.Itoa(change.TargetGiB)}}
	var response struct{ RequestId, OrderId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "ResizeDisk", "2014-05-26", query, &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	if response.OrderId != "" {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) resizeHuaweiDisk(ctx context.Context, region string, change provider.VolumeResize) (string, error) {
	endpoint := url.URL{Scheme: "https", Host: "evs." + region + ".myhuaweicloud.com",
		Path: "/v2.1/" + c.profile.ProjectID + "/cloudvolumes/" + change.ID + "/action"}
	var input struct {
		Extend struct {
			NewSize int `json:"new_size"`
		} `json:"os-extend"`
	}
	input.Extend.NewSize = change.TargetGiB
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var response struct {
		JobID   string `json:"job_id"`
		OrderID string `json:"order_id"`
	}
	if err := c.request(req, &response, http.StatusAccepted); err != nil {
		return "", err
	}
	if response.OrderID != "" || !resourceID.MatchString(response.JobID) {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return response.JobID, nil
}
