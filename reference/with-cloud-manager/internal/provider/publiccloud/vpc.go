package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

const networkIntentTag = "plumego-cmp-network"

// 完整且为空的列表与厂商错误分别处理。
var emptyNetworkList = &provider.Failure{Kind: provider.OutcomeUnknown}

func (c *Client) checkNetworkScope(scope provider.Scope, spec provider.NetworkSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if !slices.Contains(c.profile.Regions, scope.Region) || !launchTokenPattern.MatchString(token) {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	if scope.Kind == provider.ElasticIP {
		if spec.ValidPublicCloudElasticIP(c.name) {
			return nil
		}
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	if spec.BandwidthMbps != 0 || spec.AddressPool != "" {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	if scope.Kind == provider.Network && spec.ValidPublicCloudVPC(c.name) {
		return nil
	}
	if scope.Kind == provider.Subnet && spec.ValidPublicCloudSubnet(c.name, scope.Region) {
		if (c.name == provider.HuaweiECS && huaweiResourceID.MatchString(spec.ParentID)) ||
			(c.name != provider.HuaweiECS && validLaunchID(spec.ParentID, "vpc-")) {
			return nil
		}
	}
	if scope.Kind == provider.SecurityGroup && spec.ValidPublicCloudSecurityGroup(c.name) &&
		(c.name != provider.AlibabaECS || validLaunchID(spec.ParentID, "vpc-")) {
		return nil
	}
	if scope.Kind == provider.SecurityGroupRule && spec.Name == "" && spec.CIDR == "" && spec.Zone == "" &&
		spec.Rule != nil && spec.Rule.Valid() && c.validRuleGroup(spec.ParentID) {
		return nil
	}
	return &provider.Failure{Kind: provider.CreationBlocked}
}

func vpcMarker(scope provider.Scope, token string) string {
	return sha([]byte(networkIntentTag + "\x00" + scope.Provider + "\x00" + scope.RemoteIdentity + "\x00" + scope.Region + "\x00" + token))
}

func (c *Client) CreateNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	send, err := c.PrepareNetworkCreation(ctx, scope, spec, token)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	return send(ctx)
}

func (c *Client) PrepareNetworkCreation(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkNetworkScope(scope, spec, token); err != nil {
		return nil, err
	}
	if scope.Kind == provider.SecurityGroup {
		return c.prepareGroupCreation(ctx, scope, spec, token)
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.prepareRuleCreation(ctx, scope, spec, token)
	}
	if scope.Kind == provider.ElasticIP {
		return c.prepareElasticIPCreation(scope, spec, token)
	}
	if !c.profile.AllowNetworkCreation {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	marker := vpcMarker(scope, token)
	if scope.Kind == provider.Subnet {
		if err := c.checkSubnetParent(ctx, scope.Region, spec); err != nil {
			return nil, err
		}
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createSubnet(ctx, scope.Region, spec, token, marker)
		}, nil
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		return c.createVPC(ctx, scope.Region, spec, token, marker)
	}, nil
}

func (c *Client) createVPC(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.createTencentVPC(ctx, region, spec, marker)
	case provider.AlibabaECS:
		return c.createAlibabaVPC(ctx, region, spec, token, marker)
	default:
		return c.createHuaweiVPC(ctx, region, spec, networkIntentTag+":"+marker)
	}
}

func (c *Client) FindCreatedNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token, id string) (provider.Object, error) {
	if err := c.checkNetworkScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	marker := vpcMarker(scope, token)
	if scope.Kind == provider.Subnet {
		return c.findCreatedSubnet(ctx, scope.Region, spec, id, marker)
	}
	if scope.Kind == provider.SecurityGroup {
		return c.findCreatedGroup(ctx, scope.Region, spec, id, marker)
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.findCreatedRule(ctx, scope.Region, spec, id, marker)
	}
	if scope.Kind == provider.ElasticIP {
		return c.findCreatedElasticIP(ctx, scope.Region, spec, id, marker)
	}
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentVPC(ctx, scope.Region, spec, id, marker)
	case provider.AlibabaECS:
		return c.findAlibabaVPC(ctx, scope.Region, spec, id, marker)
	default:
		return c.findHuaweiVPC(ctx, scope.Region, spec, id, networkIntentTag+":"+marker)
	}
}

func (c *Client) createTencentVPC(ctx context.Context, region string, spec provider.NetworkSpec, marker string) (provider.LaunchReceipt, error) {
	payload := struct {
		VpcName, CidrBlock string
		Tags               []tag
	}{spec.Name, spec.CIDR, []tag{{Key: networkIntentTag, Value: marker}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		RequestId string
		Vpc       struct{ VpcId string }
	}
	if err := c.tencent(ctx, "vpc", region, "CreateVpc", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt([]string{result.Vpc.VpcId}, result.RequestId, "vpc-")
}

func (c *Client) findTencentVPC(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	row, err := c.readTencentVPC(ctx, region, id, marker)
	if err != nil {
		return provider.Object{}, err
	}
	if row.VpcName != spec.Name || row.CidrBlock != spec.CIDR || !matchingTag(row.TagSet, networkIntentTag, marker) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return tagObject(row.VpcId, row.VpcName, "available", row.TagSet), nil
}

type tencentNetworkFilter struct {
	Name   string
	Values []string
}

type tencentVPCObservation struct {
	VpcId, VpcName, CidrBlock string
	IsDefault                 *bool
	TagSet                    []*tag
	AssistantCidrSet          []struct{ CidrBlock string }
}

func (c *Client) readTencentVPC(ctx context.Context, region, id, marker string) (tencentVPCObservation, error) {
	payload := struct {
		Limit   string
		VpcIds  []string               `json:",omitempty"`
		Filters []tencentNetworkFilter `json:",omitempty"`
	}{Limit: "2"}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		payload.Filters = []tencentNetworkFilter{{"tag:" + networkIntentTag, []string{marker}}}
	} else {
		if !validLaunchID(id, "vpc-") {
			return tencentVPCObservation{}, invalid
		}
		payload.VpcIds = []string{id}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return tencentVPCObservation{}, err
	}
	var result struct {
		TotalCount *int
		VpcSet     []tencentVPCObservation
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeVpcs", "2017-03-12", body, &result); err != nil {
		return tencentVPCObservation{}, err
	}
	if result.TotalCount == nil || result.VpcSet == nil || *result.TotalCount != len(result.VpcSet) || len(result.VpcSet) > 1 {
		return tencentVPCObservation{}, invalid
	}
	if len(result.VpcSet) == 0 {
		return tencentVPCObservation{}, emptyNetworkList
	}
	row := result.VpcSet[0]
	if !validLaunchID(row.VpcId, "vpc-") || (id != "" && row.VpcId != id) {
		return tencentVPCObservation{}, invalid
	}
	return row, nil
}

func alibabaVPCHost(region string) string {
	if region == "cn-hangzhou" {
		return "vpc.aliyuncs.com"
	}
	return "vpc." + region + ".aliyuncs.com"
}

func (c *Client) createAlibabaVPC(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "VpcName": {spec.Name}, "CidrBlock": {spec.CIDR},
		"EnableIpv6": {"false"}, "ClientToken": {token}, "Tag.1.Key": {networkIntentTag}, "Tag.1.Value": {marker}}
	var result struct{ RequestId, VpcId, Code string }
	if err := c.alibaba(ctx, alibabaVPCHost(region), "CreateVpc", "2016-04-28", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return creationReceipt([]string{result.VpcId}, result.RequestId, "vpc-")
}

func (c *Client) findAlibabaVPC(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	row, err := c.readAlibabaVPC(ctx, region, id, marker)
	if err != nil {
		return provider.Object{}, err
	}
	if row.VpcName != spec.Name || row.CidrBlock != spec.CIDR || !matchingTag(row.Tags.Tag, networkIntentTag, marker) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return tagObject(row.VpcId, row.VpcName, row.Status, row.Tags.Tag), nil
}

type alibabaVPCObservation struct {
	VpcId, VpcName, CidrBlock, RegionId, Status string
	OwnerId                                     json.Number
	IsDefault                                   *bool
	Tags                                        struct{ Tag []*tag }
	SecondaryCidrBlocks                         struct{ SecondaryCidrBlock []string }
}

func (c *Client) readAlibabaVPC(ctx context.Context, region, id, marker string) (alibabaVPCObservation, error) {
	query := url.Values{"RegionId": {region}, "VpcOwnerId": {c.profile.AccountID}, "PageNumber": {"1"}, "PageSize": {"10"}}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("Tag.1.Key", networkIntentTag)
		query.Set("Tag.1.Value", marker)
	} else {
		if !validLaunchID(id, "vpc-") {
			return alibabaVPCObservation{}, invalid
		}
		query.Set("VpcId", id)
	}
	var result struct {
		alibabaNumberedPage
		Code string
		Vpcs struct{ Vpc []alibabaVPCObservation }
	}
	if err := c.alibaba(ctx, alibabaVPCHost(region), "DescribeVpcs", "2016-04-28", query, &result); err != nil {
		return alibabaVPCObservation{}, err
	}
	if result.Code != "" {
		return alibabaVPCObservation{}, cloudFailure(result.Code)
	}
	rows := result.Vpcs.Vpc
	next, err := result.next(1, 10, len(rows))
	if err != nil || next != "" || rows == nil || len(rows) > 1 {
		return alibabaVPCObservation{}, invalid
	}
	if len(rows) == 0 {
		return alibabaVPCObservation{}, emptyNetworkList
	}
	row := rows[0]
	if row.OwnerId.String() != c.profile.AccountID || row.RegionId != region {
		return alibabaVPCObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !validLaunchID(row.VpcId, "vpc-") || (id != "" && row.VpcId != id) || row.Status == "" {
		return alibabaVPCObservation{}, invalid
	}
	return row, nil
}

func (c *Client) createHuaweiVPC(ctx context.Context, region string, spec provider.NetworkSpec, description string) (provider.LaunchReceipt, error) {
	type vpc struct {
		Name        string `json:"name"`
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
	}
	payload := struct {
		VPC vpc `json:"vpc"`
	}{vpc{spec.Name, spec.CIDR, description}}
	var result struct {
		RequestID string               `json:"request_id"`
		VPC       huaweiNetworkReceipt `json:"vpc"`
	}
	if err := c.postHuaweiNetwork(ctx, region, "/v1/"+c.profile.ProjectID+"/vpcs", payload, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return c.huaweiNetworkReceipt(result.RequestID, result.VPC)
}

func (c *Client) postHuaweiNetwork(ctx context.Context, region, path string, payload, dst any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com", Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	return c.request(req, dst, http.StatusCreated)
}

type huaweiNetworkReceipt struct {
	ID     string
	Tenant string `json:"tenant_id"`
}

func (c *Client) huaweiNetworkReceipt(requestID string, resource huaweiNetworkReceipt) (provider.LaunchReceipt, error) {
	if resource.Tenant != "" && resource.Tenant != c.profile.ProjectID {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !huaweiResourceID.MatchString(resource.ID) {
		return provider.LaunchReceipt{RequestID: requestID}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.LaunchReceipt{RequestID: requestID, ResourceID: resource.ID}, nil
}

func (c *Client) findHuaweiVPC(ctx context.Context, region string, spec provider.NetworkSpec, id, description string) (provider.Object, error) {
	row, err := c.readHuaweiVPC(ctx, region, id, description)
	if err != nil {
		return provider.Object{}, err
	}
	if row.Name != spec.Name || row.CIDR != spec.CIDR || row.Description != description {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return tagObject(row.ID, row.Name, row.Status, row.Tags), nil
}

type huaweiVPCObservation struct {
	ID, Name, Status, CIDR, Description string
	Project                             string   `json:"project_id"`
	ExtraCIDRs                          []string `json:"extend_cidrs"`
	Tags                                []*tag
}

type huaweiPageInfo struct {
	Next  string `json:"next_marker"`
	Count *int   `json:"current_count"`
}

func (p *huaweiPageInfo) isCompleteSingleton(count int) bool {
	return p != nil && count <= 1 && p.Next == "" && p.Count != nil && *p.Count == count
}

func (c *Client) readHuaweiVPC(ctx context.Context, region, id, description string) (huaweiVPCObservation, error) {
	query := url.Values{"limit": {"2"}}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("description", description)
	} else {
		if !huaweiResourceID.MatchString(id) {
			return huaweiVPCObservation{}, invalid
		}
		query.Set("id", id)
	}
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/vpcs", RawQuery: query.Encode()}
	var result struct {
		VPCs     []huaweiVPCObservation
		PageInfo *huaweiPageInfo `json:"page_info"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return huaweiVPCObservation{}, err
	}
	if result.VPCs == nil || !result.PageInfo.isCompleteSingleton(len(result.VPCs)) {
		return huaweiVPCObservation{}, invalid
	}
	if len(result.VPCs) == 0 {
		return huaweiVPCObservation{}, emptyNetworkList
	}
	row := result.VPCs[0]
	if row.Project != c.profile.ProjectID {
		return huaweiVPCObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !huaweiResourceID.MatchString(row.ID) || (id != "" && row.ID != id) || row.Status == "" {
		return huaweiVPCObservation{}, invalid
	}
	return row, nil
}
