package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) readGroupDeletionState(ctx context.Context, region string, target provider.NetworkDeletionTarget) (networkDeletionState, error) {
	switch c.name {
	case provider.TencentCVM:
		row, err := c.readTencentGroup(ctx, region, target.ID, "")
		if err == emptyNetworkList {
			return networkDeletionState{}, nil
		}
		if err != nil {
			return networkDeletionState{}, err
		}
		return networkDeletionState{present: true, deletable: !*row.IsDefault && (row.ServiceManaged == nil || !*row.ServiceManaged)}, nil
	case provider.AlibabaECS:
		row, err := c.readAlibabaGroup(ctx, region, "", target.ID, "")
		if err == emptyNetworkList {
			return networkDeletionState{}, nil
		}
		if err != nil {
			return networkDeletionState{}, err
		}
		if row.VpcId != target.ParentID {
			return networkDeletionState{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		deletable := row.SecurityGroupType == "enterprise" && row.ServiceManaged != nil && !*row.ServiceManaged &&
			row.SecurityGroupName != "" && !strings.EqualFold(row.SecurityGroupName, "default")
		return networkDeletionState{present: true, deletable: deletable}, nil
	default:
		endpoint := c.huaweiGroupEndpoint(region, "/v3/"+c.profile.ProjectID+"/vpc/security-groups")
		endpoint.RawQuery = url.Values{"id": {target.ID}, "limit": {"2"}}.Encode()
		var result struct {
			Groups   []huaweiGroupObservation `json:"security_groups"`
			PageInfo *huaweiPageInfo          `json:"page_info"`
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return networkDeletionState{}, err
		}
		if result.Groups == nil || !result.PageInfo.isCompleteSingleton(len(result.Groups)) {
			return networkDeletionState{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if len(result.Groups) == 0 {
			return networkDeletionState{}, nil
		}
		row := result.Groups[0]
		if row.ID != target.ID || row.Project != c.profile.ProjectID {
			return networkDeletionState{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		return networkDeletionState{present: true, deletable: row.Name != "" && !strings.EqualFold(row.Name, "default")}, nil
	}
}

func (c *Client) checkGroupDependencies(ctx context.Context, region, id string) error {
	switch c.name {
	case provider.TencentCVM:
		return c.checkTencentGroupDependencies(ctx, region, id)
	case provider.AlibabaECS:
		return c.checkAlibabaGroupDependencies(ctx, region, id)
	default:
		return c.checkHuaweiGroupDependencies(ctx, region, id)
	}
}

func (c *Client) checkTencentGroupDependencies(ctx context.Context, region, id string) error {
	body, err := json.Marshal(struct{ SecurityGroupIds []string }{[]string{id}})
	if err != nil {
		return err
	}
	var result struct {
		SecurityGroupAssociationStatisticsSet []struct {
			SecurityGroupId                    string
			TotalCount, SG, CVM, CDB, ENI, CLB *int64
			InstanceStatistics                 []struct{ InstanceCount *int64 }
		}
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSecurityGroupAssociationStatistics", "2017-03-12", body, &result); err != nil {
		return err
	}
	rows := result.SecurityGroupAssociationStatisticsSet
	if len(rows) != 1 || rows[0].SecurityGroupId != id || rows[0].TotalCount == nil || rows[0].SG == nil ||
		*rows[0].TotalCount < 0 || *rows[0].SG < 0 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	row := rows[0]
	counts := []*int64{row.TotalCount, row.SG, row.CVM, row.CDB, row.ENI, row.CLB}
	for _, instance := range row.InstanceStatistics {
		if instance.InstanceCount == nil {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		counts = append(counts, instance.InstanceCount)
	}
	for _, count := range counts {
		if count != nil && *count < 0 {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		if count != nil && *count > 0 {
			return &provider.Failure{Kind: provider.DeletionBlocked}
		}
	}
	return nil
}

func (c *Client) checkAlibabaGroupDependencies(ctx context.Context, region, id string) error {
	host := "ecs." + region + ".aliyuncs.com"
	var refs struct {
		Code                    string
		SecurityGroupReferences struct {
			SecurityGroupReference []struct {
				SecurityGroupId           string
				ReferencingSecurityGroups struct {
					ReferencingSecurityGroup []struct{ SecurityGroupId, AliUid string }
				}
			}
		}
	}
	query := url.Values{"RegionId": {region}, "SecurityGroupId.1": {id}}
	if err := c.alibaba(ctx, host, "DescribeSecurityGroupReferences", "2014-05-26", query, &refs); err != nil {
		return err
	}
	if refs.Code != "" {
		return cloudFailure(refs.Code)
	}
	rows := refs.SecurityGroupReferences.SecurityGroupReference
	if len(rows) != 1 || rows[0].SecurityGroupId != id || rows[0].ReferencingSecurityGroups.ReferencingSecurityGroup == nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, row := range rows[0].ReferencingSecurityGroups.ReferencingSecurityGroup {
		if row.SecurityGroupId != id || row.AliUid != c.profile.AccountID {
			return &provider.Failure{Kind: provider.DeletionBlocked}
		}
	}
	query = url.Values{"RegionId": {region}, "SecurityGroupId": {id}, "MaxResults": {"10"}}
	var instances struct {
		Code, NextToken string
		Instances       struct{ Instance []struct{ InstanceId string } }
	}
	if err := c.alibaba(ctx, host, "DescribeInstances", "2014-05-26", query, &instances); err != nil {
		return err
	}
	if instances.Code != "" {
		return cloudFailure(instances.Code)
	}
	if err := requireNoGroupBindings(instances.Instances.Instance != nil, len(instances.Instances.Instance), instances.NextToken != ""); err != nil {
		return err
	}
	var interfaces struct {
		Code, NextToken      string
		NetworkInterfaceSets struct {
			NetworkInterfaceSet []struct{ NetworkInterfaceId string }
		}
	}
	if err := c.alibaba(ctx, host, "DescribeNetworkInterfaces", "2014-05-26", query, &interfaces); err != nil {
		return err
	}
	if interfaces.Code != "" {
		return cloudFailure(interfaces.Code)
	}
	enis := interfaces.NetworkInterfaceSets.NetworkInterfaceSet
	return requireNoGroupBindings(enis != nil, len(enis), interfaces.NextToken != "")
}

func (c *Client) huaweiGroupEndpoint(region, path string) url.URL {
	return url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com", Path: path}
}

func (c *Client) checkHuaweiGroupDependencies(ctx context.Context, region, id string) error {
	endpoint := c.huaweiGroupEndpoint(region, "/v1/"+c.profile.ProjectID+"/ports")
	endpoint.RawQuery = url.Values{"security_groups": {id}, "limit": {"1"}}.Encode()
	var ports struct {
		Ports []struct{ ID string }
		Links []struct{ Rel string } `json:"ports_links"`
	}
	if err := c.readHuawei(ctx, endpoint, &ports); err != nil {
		return err
	}
	if err := requireNoGroupBindings(ports.Ports != nil, len(ports.Ports), len(ports.Links) != 0); err != nil {
		return err
	}
	endpoint.Path = "/v3/" + c.profile.ProjectID + "/vpc/security-group-rules"
	query := url.Values{"remote_group_id": {id}, "limit": {"100"}}
	seen := make(map[string]struct{})
	for range 8 {
		endpoint.RawQuery = query.Encode()
		var result struct {
			Rules    []huaweiRule    `json:"security_group_rules"`
			PageInfo *huaweiPageInfo `json:"page_info"`
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return err
		}
		if result.Rules == nil || len(result.Rules) > 100 || result.PageInfo == nil ||
			result.PageInfo.Count == nil || *result.PageInfo.Count != len(result.Rules) {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, rule := range result.Rules {
			if !huaweiResourceID.MatchString(rule.ID) || rule.RemoteGroup != id {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
			if rule.GroupID != id || rule.ProjectID != c.profile.ProjectID {
				return &provider.Failure{Kind: provider.DeletionBlocked}
			}
			if _, exists := seen[rule.ID]; exists {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
			seen[rule.ID] = struct{}{}
		}
		next := result.PageInfo.Next
		if next == "" {
			return nil
		}
		if len(result.Rules) == 0 || next != result.Rules[len(result.Rules)-1].ID {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		query.Set("marker", next)
	}
	return &provider.Failure{Kind: provider.InvalidResponse}
}

func requireNoGroupBindings(completeArray bool, count int, hasNext bool) error {
	if !completeArray || (count == 0 && hasNext) {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if count != 0 {
		return &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return nil
}

func (c *Client) deleteSecurityGroup(ctx context.Context, region, id string) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct{ SecurityGroupId string }{id})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "vpc", region, "DeleteSecurityGroup", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		query := url.Values{"RegionId": {region}, "SecurityGroupId": {id}}
		var result struct{ RequestId, Code string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DeleteSecurityGroup", "2014-05-26", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		endpoint := c.huaweiGroupEndpoint(region, "/v3/"+c.profile.ProjectID+"/vpc/security-groups/"+id)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusNoContent)
	}
}
