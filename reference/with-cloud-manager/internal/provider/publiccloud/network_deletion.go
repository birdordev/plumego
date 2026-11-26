package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

type networkDeletionState struct {
	present   bool
	deletable bool
}

func (c *Client) checkNetworkDeletionScope(scope provider.Scope, target provider.NetworkDeletionTarget) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity || !slices.Contains(c.profile.Regions, scope.Region) {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if !target.Valid(c.name, scope.Kind) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) PrepareNetworkDeletion(ctx context.Context, scope provider.Scope, target provider.NetworkDeletionTarget) (func(context.Context) (string, error), error) {
	if err := c.checkNetworkDeletionScope(scope, target); err != nil {
		return nil, err
	}
	if (scope.Kind == provider.Network && !c.profile.AllowVPCDeletion) ||
		(scope.Kind == provider.Subnet && !c.profile.AllowSubnetDeletion) ||
		(scope.Kind == provider.SecurityGroup && !c.profile.AllowSecurityGroupDeletion) {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	state, err := c.readNetworkDeletionState(ctx, scope, target)
	if err != nil {
		return nil, err
	}
	if !state.deletable {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	if scope.Kind == provider.SecurityGroup {
		if err := c.checkGroupDependencies(ctx, scope.Region, target.ID); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (string, error) {
			return c.deleteSecurityGroup(ctx, scope.Region, target.ID)
		}, nil
	}
	if scope.Kind == provider.Network {
		// 只允许删除云端确认没有子网的 VPC。
		children, err := c.readNetworkSubnets(ctx, scope.Region, "", target.ID)
		if err != nil {
			return nil, err
		}
		if children.present {
			return nil, &provider.Failure{Kind: provider.DeletionBlocked}
		}
	}
	return func(ctx context.Context) (string, error) {
		return c.deleteNetwork(ctx, scope.Region, target)
	}, nil
}

func (c *Client) ObserveNetworkDeletion(ctx context.Context, scope provider.Scope, target provider.NetworkDeletionTarget) (provider.ActionResult, error) {
	if err := c.checkNetworkDeletionScope(scope, target); err != nil {
		return provider.ActionPending, err
	}
	state, err := c.readNetworkDeletionState(ctx, scope, target)
	if err != nil || state.present {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) readNetworkDeletionState(ctx context.Context, scope provider.Scope, target provider.NetworkDeletionTarget) (networkDeletionState, error) {
	if scope.Kind == provider.SecurityGroup {
		return c.readGroupDeletionState(ctx, scope.Region, target)
	}
	if scope.Kind == provider.Subnet {
		return c.readNetworkSubnets(ctx, scope.Region, target.ID, target.ParentID)
	}
	var state networkDeletionState
	var err error
	switch c.name {
	case provider.TencentCVM:
		var row tencentVPCObservation
		row, err = c.readTencentVPC(ctx, scope.Region, target.ID, "")
		state = networkDeletionState{present: err == nil, deletable: row.IsDefault != nil && !*row.IsDefault}
	case provider.AlibabaECS:
		var row alibabaVPCObservation
		row, err = c.readAlibabaVPC(ctx, scope.Region, target.ID, "")
		state = networkDeletionState{present: err == nil, deletable: row.Status == "Available" && row.IsDefault != nil && !*row.IsDefault}
	case provider.HuaweiECS:
		var row huaweiVPCObservation
		row, err = c.readHuaweiVPC(ctx, scope.Region, target.ID, "")
		state = networkDeletionState{present: err == nil, deletable: row.Status == "ACTIVE"}
	}
	if err == emptyNetworkList {
		return networkDeletionState{}, nil
	}
	return state, err
}

func (c *Client) readNetworkSubnets(ctx context.Context, region, id, parent string) (networkDeletionState, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.readTencentNetworkSubnets(ctx, region, id, parent)
	case provider.AlibabaECS:
		return c.readAlibabaNetworkSubnets(ctx, region, id, parent)
	default:
		return c.readHuaweiNetworkSubnets(ctx, region, id, parent)
	}
}

func (c *Client) readTencentNetworkSubnets(ctx context.Context, region, id, parent string) (networkDeletionState, error) {
	payload := struct {
		Limit     string
		SubnetIds []string               `json:",omitempty"`
		Filters   []tencentNetworkFilter `json:",omitempty"`
	}{Limit: "2"}
	if id != "" {
		payload.SubnetIds = []string{id}
	} else {
		payload.Filters = []tencentNetworkFilter{{Name: "vpc-id", Values: []string{parent}}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return networkDeletionState{}, err
	}
	var result struct {
		TotalCount *int
		SubnetSet  []struct {
			SubnetId, VpcId string
			IsDefault       *bool
		}
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSubnets", "2017-03-12", body, &result); err != nil {
		return networkDeletionState{}, err
	}
	rows := result.SubnetSet
	if result.TotalCount == nil || rows == nil {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if id == "" && *result.TotalCount > 0 {
		return networkDeletionState{present: true}, nil
	}
	if *result.TotalCount != len(rows) || len(rows) > 1 {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return networkDeletionState{}, nil
	}
	row := rows[0]
	if row.SubnetId != id || row.VpcId != parent {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return networkDeletionState{present: true, deletable: row.IsDefault != nil && !*row.IsDefault}, nil
}

func (c *Client) readAlibabaNetworkSubnets(ctx context.Context, region, id, parent string) (networkDeletionState, error) {
	query := url.Values{"RegionId": {region}, "PageNumber": {"1"}, "PageSize": {"10"}}
	if id != "" {
		query.Set("VSwitchId", id)
	} else {
		// 查询 VPC 下的完整集合，包括共享子网。
		query.Set("VpcId", parent)
	}
	var result struct {
		alibabaNumberedPage
		Code      string
		VSwitches struct {
			VSwitch []struct {
				VSwitchId, VpcId, Status, ShareType string
				OwnerId                             json.Number
				IsDefault                           *bool
			}
		}
	}
	if err := c.alibaba(ctx, alibabaVPCHost(region), "DescribeVSwitches", "2016-04-28", query, &result); err != nil {
		return networkDeletionState{}, err
	}
	if result.Code != "" {
		return networkDeletionState{}, cloudFailure(result.Code)
	}
	rows := result.VSwitches.VSwitch
	next, err := result.next(1, 10, len(rows))
	if err != nil || rows == nil {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if id == "" && len(rows) > 0 {
		return networkDeletionState{present: true}, nil
	}
	if next != "" || len(rows) > 1 {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return networkDeletionState{}, nil
	}
	row := rows[0]
	if row.VSwitchId != id || row.VpcId != parent || row.OwnerId.String() != c.profile.AccountID {
		return networkDeletionState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return networkDeletionState{present: true, deletable: row.Status == "Available" && row.ShareType == "" && row.IsDefault != nil && !*row.IsDefault}, nil
}

func (c *Client) readHuaweiNetworkSubnets(ctx context.Context, region, id, parent string) (networkDeletionState, error) {
	query := url.Values{"limit": {"2"}}
	if id != "" {
		query.Set("id", id)
	} else {
		query.Set("vpc_id", parent)
	}
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/virsubnets", RawQuery: query.Encode()}
	var result struct {
		Subnets []struct {
			ID, Status string
			Project    string `json:"project_id"`
			Parent     string `json:"vpc_id"`
		} `json:"virsubnets"`
		PageInfo *huaweiPageInfo `json:"page_info"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return networkDeletionState{}, err
	}
	if id == "" && len(result.Subnets) > 0 {
		return networkDeletionState{present: true}, nil
	}
	if result.Subnets == nil || !result.PageInfo.isCompleteSingleton(len(result.Subnets)) {
		return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Subnets) == 0 {
		return networkDeletionState{}, nil
	}
	row := result.Subnets[0]
	if row.ID != id || row.Parent != parent || row.Project != c.profile.ProjectID {
		return networkDeletionState{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return networkDeletionState{present: true, deletable: row.Status == "ACTIVE"}, nil
}

func (c *Client) deleteNetwork(ctx context.Context, region string, target provider.NetworkDeletionTarget) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		action, field := "DeleteVpc", "VpcId"
		if target.ParentID != "" {
			action, field = "DeleteSubnet", "SubnetId"
		}
		body, err := json.Marshal(map[string]string{field: target.ID})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "vpc", region, action, "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		action := "DeleteVpc"
		query := url.Values{"RegionId": {region}}
		if target.ParentID == "" {
			query.Set("VpcId", target.ID)
			query.Set("ForceDelete", "false")
		} else {
			action = "DeleteVSwitch"
			query.Set("VSwitchId", target.ID)
		}
		var result struct{ Code, RequestId string }
		if err := c.alibaba(ctx, alibabaVPCHost(region), action, "2016-04-28", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		path := "/v1/" + c.profile.ProjectID + "/vpcs/" + target.ID
		if target.ParentID != "" {
			path = "/v1/" + c.profile.ProjectID + "/vpcs/" + target.ParentID + "/subnets/" + target.ID
		}
		endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com", Path: path}
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusNoContent)
	}
}
