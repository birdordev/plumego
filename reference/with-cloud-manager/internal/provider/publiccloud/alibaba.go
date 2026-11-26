package publiccloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) alibaba(ctx context.Context, host, action, version string, query url.Values, dst any) error {
	return c.alibabaRPC(ctx, host, action, version, query, "", dst)
}

func (c *Client) alibabaForm(ctx context.Context, host, action, version string, fields url.Values, dst any) error {
	return c.alibabaRPC(ctx, host, action, version, nil, fields.Encode(), dst)
}

func (c *Client) alibabaRPC(ctx context.Context, host, action, version string, query url.Values, form string, dst any) error {
	return c.alibabaSigned(ctx, http.MethodPost, host, "/", action, version, query, form, dst)
}

func (c *Client) alibabaSigned(ctx context.Context, method, host, path, action, version string, query url.Values, form string, dst any) error {
	endpoint := url.URL{Scheme: "https", Host: host, Path: path, RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20")}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), strings.NewReader(form))
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	headers := map[string]string{
		"host": host, "x-acs-action": action, "x-acs-version": version, "x-acs-content-sha256": sha([]byte(form)),
		"x-acs-date": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "x-acs-signature-nonce": rand.Text(),
	}
	if c.profile.SecurityToken != "" {
		headers["x-acs-security-token"] = c.profile.SecurityToken
	}
	if form != "" {
		headers["content-type"] = "application/x-www-form-urlencoded"
	}
	keys := make([]string, 0, len(headers))
	for name := range headers {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	var flattened strings.Builder
	for _, name := range keys {
		flattened.WriteString(name + ":" + headers[name] + "\n")
		if name != "host" {
			req.Header.Set(name, headers[name])
		}
	}
	signed := strings.Join(keys, ";")
	canonical := method + "\n" + endpoint.EscapedPath() + "\n" + endpoint.RawQuery + "\n" + flattened.String() + "\n" + signed + "\n" + headers["x-acs-content-sha256"]
	const algorithm = "ACS3-HMAC-SHA256"
	sig := hex.EncodeToString(mac([]byte(c.profile.AccessKeySecret), algorithm+"\n"+sha([]byte(canonical))))
	req.Header.Set("Authorization", algorithm+" Credential="+c.profile.AccessKeyID+",SignedHeaders="+signed+",Signature="+sig)
	return c.request(req, dst)
}

func (c *Client) alibabaIdentity(ctx context.Context) (string, error) {
	var result struct {
		AccountID string `json:"AccountId"`
		Code      string
	}
	err := c.alibaba(ctx, "sts.aliyuncs.com", "GetCallerIdentity", "2015-04-01", nil, &result)
	if err == nil && result.Code != "" {
		err = cloudFailure(result.Code)
	}
	return result.AccountID, err
}

func (c *Client) alibabaInstances(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, NextToken string
		Instances       struct{ Instance []alibabaInstanceObservation }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeInstances", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	if result.Instances.Instance == nil || len(result.Instances.Instance) > pageSize || (len(result.Instances.Instance) == 0 && result.NextToken != "") {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: result.NextToken}
	for _, row := range result.Instances.Instance {
		if row.RegionId != region || !strings.HasPrefix(row.InstanceId, "i-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, row.object())
	}
	return page, nil
}

func (row alibabaInstanceObservation) object() provider.Object {
	item := tagObject(row.InstanceId, row.InstanceName, row.Status, row.Tags.normalized())
	item.Link(provider.Zone, row.ZoneId, "in_zone")
	item.Link(provider.Network, row.VpcAttributes.VpcId, "in_network")
	item.Link(provider.Subnet, row.VpcAttributes.VSwitchId, "in_subnet")
	item.Link(provider.Image, row.ImageId, "uses_image")
	for _, group := range row.SecurityGroupIds.SecurityGroupId {
		item.Link(provider.SecurityGroup, group, "uses_security_group")
	}
	return item
}

type alibabaECSTags struct {
	Tag []struct{ TagKey, TagValue string }
}

func (tags alibabaECSTags) normalized() []*tag {
	if tags.Tag == nil {
		return nil
	}
	result := make([]*tag, 0, len(tags.Tag))
	for _, value := range tags.Tag {
		result = append(result, &tag{Key: value.TagKey, Value: value.TagValue})
	}
	return result
}

func (c *Client) alibabaNetworking(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Kind == provider.SecurityGroup {
		return c.alibabaSecurityGroups(ctx, scope.Region, cursor, pageSize)
	}
	const vpcPageSize = 50
	number, err := pageNumber(cursor, 1)
	if err != nil {
		return provider.Page{}, err
	}
	query := url.Values{
		"RegionId": {scope.Region}, "PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(vpcPageSize)},
	}
	action := "DescribeVpcs"
	if scope.Kind == provider.Subnet {
		action = "DescribeVSwitches"
		query.Set("VSwitchOwnerId", c.profile.AccountID)
	} else {
		query.Set("VpcOwnerId", c.profile.AccountID)
	}
	host := alibabaVPCHost(scope.Region)
	type vpcRow struct {
		VpcId, VpcName, VSwitchId, VSwitchName, Status, RegionId, ZoneId string
		OwnerId                                                          json.Number
		Tags                                                             struct{ Tag []*tag }
	}
	var result struct {
		alibabaNumberedPage
		Code      string
		Vpcs      struct{ Vpc []vpcRow }
		VSwitches struct{ VSwitch []vpcRow }
	}
	if err := c.alibaba(ctx, host, action, "2016-04-28", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}

	rows := result.Vpcs.Vpc
	if scope.Kind == provider.Subnet {
		rows = result.VSwitches.VSwitch
	}
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := result.next(number, vpcPageSize, len(rows))
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.OwnerId.String() != c.profile.AccountID || (row.RegionId != "" && row.RegionId != scope.Region) {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !strings.HasPrefix(row.VpcId, "vpc-") || !resourceID.MatchString(row.VpcId) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if scope.Kind == provider.Network {
			page.Objects = append(page.Objects, tagObject(row.VpcId, row.VpcName, row.Status, row.Tags.Tag))
			continue
		}
		if !strings.HasPrefix(row.VSwitchId, "vsw-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := tagObject(row.VSwitchId, row.VSwitchName, row.Status, row.Tags.Tag)
		item.Link(provider.Network, row.VpcId, "in_network")
		item.Link(provider.Zone, row.ZoneId, "in_zone")
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}

func (c *Client) alibabaSecurityGroups(ctx context.Context, region, cursor string, limit int) (provider.Page, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, RegionId, NextToken string
		SecurityGroups            struct {
			SecurityGroup []struct {
				SecurityGroupId, SecurityGroupName, VpcId string
				Tags                                      alibabaECSTags
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSecurityGroups", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	if result.RegionId != region {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.SecurityGroups.SecurityGroup
	if rows == nil || len(rows) > limit || (len(rows) == 0 && result.NextToken != "") {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: result.NextToken, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if !validSecurityGroupID(row.SecurityGroupId) ||
			(row.VpcId != "" && (!strings.HasPrefix(row.VpcId, "vpc-") || !resourceID.MatchString(row.VpcId))) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := tagObject(row.SecurityGroupId, row.SecurityGroupName, "observed", row.Tags.normalized())
		item.Link(provider.Network, row.VpcId, "in_network")
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}

type alibabaNumberedPage struct {
	PageNumber, PageSize int
	TotalCount           *int
}

func (p alibabaNumberedPage) next(number, size, count int) (string, error) {
	if p.TotalCount == nil || *p.TotalCount < 0 || p.PageNumber != number || p.PageSize != size || count > size {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	offset := (number - 1) * size
	if offset > *p.TotalCount || count != min(size, *p.TotalCount-offset) {
		return "", &provider.Failure{Kind: provider.ExpiredCursor}
	}
	if offset+count < *p.TotalCount {
		return strconv.Itoa(number + 1), nil
	}
	return "", nil
}
