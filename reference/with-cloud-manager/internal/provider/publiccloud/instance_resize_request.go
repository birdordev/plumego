package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) resizeTencentInstance(ctx context.Context, region string, change provider.InstanceResize) (string, error) {
	body, err := json.Marshal(struct {
		InstanceIds  []string
		InstanceType string
		ForceStop    bool
	}{InstanceIds: []string{change.ID}, InstanceType: change.TargetType})
	if err != nil {
		return "", err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "cvm", region, tencentResizeInstanceAction, "2017-03-12", body, &response); err != nil {
		return "", err
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) resizeAlibabaInstance(ctx context.Context, region string, change provider.InstanceResize) (string, error) {
	query := url.Values{"InstanceId": {change.ID}, "InstanceType": {change.TargetType}, "Async": {"false"}, "AllowMigrateAcrossZone": {"false"}}
	var response struct{ RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "ModifyInstanceSpec", "2014-05-26", query, &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) resizeHuaweiInstance(ctx context.Context, region string, change provider.InstanceResize) (string, error) {
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + change.ID + "/resize"}
	var input struct {
		Resize struct {
			Flavor string `json:"flavorRef"`
		} `json:"resize"`
	}
	input.Resize.Flavor = change.TargetType
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
