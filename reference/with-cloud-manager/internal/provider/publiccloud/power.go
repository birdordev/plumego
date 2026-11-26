package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkInstanceScope(scope provider.Scope, id string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Instance || !slices.Contains(c.profile.Regions, scope.Region) || !resourceID.MatchString(id) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	switch c.name {
	case provider.TencentCVM:
		if strings.HasPrefix(id, "ins-") && len(id) > len("ins-") {
			return nil
		}
	case provider.AlibabaECS:
		if strings.HasPrefix(id, "i-") && len(id) > len("i-") {
			return nil
		}
	case provider.HuaweiECS:
		if huaweiResourceID.MatchString(id) {
			return nil
		}
	}
	return &provider.Failure{Kind: provider.Unsupported}
}

func (c *Client) Observe(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return provider.Object{}, err
	}
	var object provider.Object
	var err error
	switch c.name {
	case provider.TencentCVM:
		object, err = c.observeTencentInstance(ctx, scope.Region, id)
	case provider.AlibabaECS:
		object, err = c.observeAlibabaInstance(ctx, scope.Region, id)
	case provider.HuaweiECS:
		object, err = c.observeHuaweiInstance(ctx, scope.Region, id)
	}
	if err != nil {
		return provider.Object{}, err
	}
	page := provider.Page{Objects: []provider.Object{object}}
	if object.ID != id || object.Status == "" || strings.ContainsRune(object.Name, 0) ||
		strings.ContainsRune(object.Status, 0) || page.Validate("") != nil {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return object, nil
}

func (c *Client) PrepareInstancePower(ctx context.Context, scope provider.Scope, id, action string) (func(context.Context) (string, error), error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return nil, err
	}
	var enabled bool
	switch action {
	case "start", "stop":
		enabled = c.profile.AllowInstancePower
	case "reboot":
		enabled = c.profile.AllowInstanceReboot
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if !enabled {
		return nil, &provider.Failure{Kind: provider.PowerDisabled}
	}
	if _, err := c.Observe(ctx, scope, id); err != nil {
		return nil, err
	}
	return func(sendCtx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.tencentPower(sendCtx, scope.Region, id, action)
		case provider.AlibabaECS:
			return c.alibabaPower(sendCtx, scope.Region, id, action)
		case provider.HuaweiECS:
			return c.huaweiPower(sendCtx, scope.Region, id, action)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ApplyInstance(ctx context.Context, scope provider.Scope, id, action string) (string, error) {
	if action == "delete" {
		send, err := c.PrepareInstanceDeletion(ctx, scope, id)
		if err != nil {
			return "", err
		}
		return send(ctx)
	}
	send, err := c.PrepareInstancePower(ctx, scope, id, action)
	if err != nil {
		return "", err
	}
	return send(ctx)
}

type tencentInstanceObservation struct {
	InstanceId, InstanceName, InstanceState                         string
	LatestOperation, LatestOperationRequestId, LatestOperationState string
	ImageId, InstanceType, InstanceChargeType                       string
	Placement                                                       struct{ Zone string }
	VirtualPrivateCloud                                             struct{ VpcId, SubnetId string }
	SystemDisk                                                      struct{ DiskId string }
	DataDisks                                                       []struct{ DiskId string }
	SecurityGroupIds                                                []string
	PublicIpAddresses                                               []string
	Tags                                                            []*tag
}

func (c *Client) observeTencentInstance(ctx context.Context, region, id string) (provider.Object, error) {
	row, err := c.describeTencentInstance(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	return provider.Object{ID: row.InstanceId, Name: row.InstanceName, Status: row.InstanceState}, nil
}

func (c *Client) describeTencentInstance(ctx context.Context, region, id string) (tencentInstanceObservation, error) {
	body, err := json.Marshal(struct{ InstanceIds []string }{[]string{id}})
	if err != nil {
		return tencentInstanceObservation{}, err
	}
	var response struct {
		TotalCount  *int
		InstanceSet []tencentInstanceObservation
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeInstances", "2017-03-12", body, &response); err != nil {
		return tencentInstanceObservation{}, err
	}
	if response.InstanceSet == nil || response.TotalCount == nil ||
		*response.TotalCount != len(response.InstanceSet) || len(response.InstanceSet) > 1 {
		return tencentInstanceObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(response.InstanceSet) == 0 {
		return tencentInstanceObservation{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := response.InstanceSet[0]
	if row.InstanceId != id {
		return tencentInstanceObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return row, nil
}

type alibabaInstanceObservation struct {
	InstanceId, InstanceName, RegionId, ZoneId, Status, InstanceChargeType string
	ImageId, InstanceType                                                  string
	VpcAttributes                                                          struct{ VpcId, VSwitchId string }
	SecurityGroupIds                                                       struct{ SecurityGroupId []string }
	PublicIpAddress                                                        struct{ IpAddress []string }
	EipAddress                                                             struct{ AllocationId, IpAddress string }
	Tags                                                                   alibabaECSTags
	OperationLocks                                                         struct {
		LockReason []struct{ LockReason string }
	}
}

func (c *Client) observeAlibabaInstance(ctx context.Context, region, id string) (provider.Object, error) {
	row, err := c.describeAlibabaInstance(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	return provider.Object{ID: row.InstanceId, Name: row.InstanceName, Status: row.Status}, nil
}

func (c *Client) describeAlibabaInstance(ctx context.Context, region, id string) (alibabaInstanceObservation, error) {
	ids, err := json.Marshal([]string{id})
	if err != nil {
		return alibabaInstanceObservation{}, err
	}
	query := url.Values{
		"RegionId": {region}, "InstanceIds": {string(ids)}, "MaxResults": {strconv.Itoa(pageSize)},
	}
	var response struct {
		Code, NextToken string
		Instances       struct {
			Instance []alibabaInstanceObservation
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeInstances", "2014-05-26", query, &response); err != nil {
		return alibabaInstanceObservation{}, err
	}
	if response.Code != "" {
		return alibabaInstanceObservation{}, cloudFailure(response.Code)
	}
	rows := response.Instances.Instance
	if rows == nil || response.NextToken != "" || len(rows) > 1 {
		return alibabaInstanceObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return alibabaInstanceObservation{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := rows[0]
	if row.RegionId != region {
		return alibabaInstanceObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if row.InstanceId != id {
		return alibabaInstanceObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return row, nil
}

type huaweiInstanceObservation struct {
	ID, Name, Status string
	Project          string `json:"tenant_id"`
	Zone             string `json:"OS-EXT-AZ:availability_zone"`
	RootDevice       string `json:"OS-EXT-SRV-ATTR:root_device_name"`
	Locked           *bool  `json:"locked"`
	Image, Flavor    struct{ ID string }
	Metadata         struct {
		VPC          string `json:"vpc_id"`
		ChargingMode string `json:"charging_mode"`
	}
	SecurityGroups []struct{ ID string } `json:"security_groups"`
	Volumes        []struct {
		ID, Device          string
		BootIndex           string          `json:"bootIndex"`
		DeleteOnTermination json.RawMessage `json:"delete_on_termination"`
	} `json:"os-extended-volumes:volumes_attached"`
}

func (c *Client) observeHuaweiInstance(ctx context.Context, region, id string) (provider.Object, error) {
	row, err := c.describeHuaweiInstance(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	return provider.Object{ID: row.ID, Name: row.Name, Status: row.Status}, nil
}

func (c *Client) describeHuaweiInstance(ctx context.Context, region, id string) (huaweiInstanceObservation, error) {
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + id}
	var response struct{ Server huaweiInstanceObservation }
	if err := c.readHuawei(ctx, u, &response); err != nil {
		return huaweiInstanceObservation{}, err
	}
	if response.Server.Project != c.profile.ProjectID {
		return huaweiInstanceObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if response.Server.ID != id {
		return huaweiInstanceObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return response.Server, nil
}

func (c *Client) tencentPower(ctx context.Context, region, id, action string) (string, error) {
	api := "StartInstances"
	input := struct {
		InstanceIds []string
		StopType    string `json:",omitempty"`
		StoppedMode string `json:",omitempty"`
	}{InstanceIds: []string{id}}
	switch action {
	case "stop":
		api, input.StopType, input.StoppedMode = "StopInstances", "SOFT", "KEEP_CHARGING"
	case "reboot":
		api, input.StopType = "RebootInstances", "SOFT"
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "cvm", region, api, "2017-03-12", body, &response); err != nil {
		return "", err
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) alibabaPower(ctx context.Context, region, id, action string) (string, error) {
	api := "StartInstance"
	query := url.Values{"InstanceId": {id}}
	switch action {
	case "stop":
		api = "StopInstance"
		query.Set("ForceStop", "false")
		query.Set("StoppedMode", "KeepCharging")
	case "reboot":
		api = "RebootInstance"
		query.Set("ForceStop", "false")
	}
	var response struct{ RequestId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", api, "2014-05-26", query, &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	return operationReceipt(response.RequestId)
}

func (c *Client) huaweiPower(ctx context.Context, region, id, action string) (string, error) {
	type target struct {
		ID string `json:"id"`
	}
	payload := struct {
		Servers []target `json:"servers"`
		Type    string   `json:"type,omitempty"`
	}{Servers: []target{{ID: id}}}
	operation := "os-" + action
	switch action {
	case "stop":
		payload.Type = "SOFT"
	case "reboot":
		operation, payload.Type = "reboot", "SOFT"
	}
	body, err := json.Marshal(map[string]any{operation: payload})
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/action"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		JobID string `json:"job_id"`
	}
	if err := c.request(req, &result); err != nil {
		return "", err
	}
	return operationReceipt(result.JobID)
}

func operationReceipt(id string) (string, error) {
	if id == "" {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return id, nil
}
