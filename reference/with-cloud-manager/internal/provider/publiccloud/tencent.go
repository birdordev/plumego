package publiccloud

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencent(ctx context.Context, service, region, action, version string, body []byte, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+service+".tencentcloudapi.com/", bytes.NewReader(body))
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	now := time.Now().UTC()
	date, timestamp := now.Format("2006-01-02"), strconv.FormatInt(now.Unix(), 10)
	const contentType = "application/json; charset=utf-8"
	const headers = "content-type;host"
	const algorithm = "TC3-HMAC-SHA256"
	canonical := "POST\n/\n\ncontent-type:" + contentType + "\nhost:" + req.URL.Host + "\n\n" + headers + "\n" + sha(body)
	scope := date + "/" + service + "/tc3_request"
	key := mac(mac(mac([]byte("TC3"+c.profile.AccessKeySecret), date), service), "tc3_request")
	sig := hex.EncodeToString(mac(key, algorithm+"\n"+timestamp+"\n"+scope+"\n"+sha([]byte(canonical))))
	req.Header.Set("Authorization", algorithm+" Credential="+c.profile.AccessKeyID+"/"+scope+", SignedHeaders="+headers+", Signature="+sig)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}
	req.Header.Set("X-TC-Timestamp", timestamp)
	if c.profile.SecurityToken != "" {
		req.Header.Set("X-TC-Token", c.profile.SecurityToken)
	}
	var envelope struct{ Response json.RawMessage }
	if err := c.request(req, &envelope); err != nil {
		return err
	}
	var response struct{ Error *struct{ Code string } }
	if len(envelope.Response) == 0 || string(envelope.Response) == "null" || json.Unmarshal(envelope.Response, &response) != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if response.Error != nil {
		if service == "cvm" && action == "DescribeImages" && response.Error.Code == "InvalidImageId.NotFound" {
			return &provider.Failure{Kind: provider.Absent}
		}
		return cloudFailure(response.Error.Code)
	}
	if json.Unmarshal(envelope.Response, dst) != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (c *Client) tencentIdentity(ctx context.Context) (string, error) {
	var result struct {
		AccountID string `json:"AccountId"`
	}
	err := c.tencent(ctx, "sts", c.profile.Regions[0], "GetCallerIdentity", "2018-08-13", []byte("{}"), &result)
	return result.AccountID, err
}

func (c *Client) tencentInstances(ctx context.Context, region, cursor string) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount  *int
		InstanceSet []tencentInstanceObservation
	}
	body, err := json.Marshal(struct{ Offset, Limit int }{offset, pageSize})
	if err != nil {
		return provider.Page{}, err
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeInstances", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	if result.InstanceSet == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.InstanceSet), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next}
	for _, row := range result.InstanceSet {
		if !strings.HasPrefix(row.InstanceId, "ins-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, row.object())
	}
	return page, nil
}

func (row tencentInstanceObservation) object() provider.Object {
	item := tagObject(row.InstanceId, row.InstanceName, row.InstanceState, row.Tags)
	item.InstanceType = row.InstanceType
	item.Link(provider.Zone, row.Placement.Zone, "in_zone")
	item.Link(provider.Network, row.VirtualPrivateCloud.VpcId, "in_network")
	item.Link(provider.Subnet, row.VirtualPrivateCloud.SubnetId, "in_subnet")
	item.Link(provider.Image, row.ImageId, "uses_image")
	item.Link(provider.Volume, row.SystemDisk.DiskId, "uses_volume")
	for _, disk := range row.DataDisks {
		item.Link(provider.Volume, disk.DiskId, "uses_volume")
	}
	for _, group := range row.SecurityGroupIds {
		item.Link(provider.SecurityGroup, group, "uses_security_group")
	}
	return item
}

func (c *Client) tencentNetworking(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Kind == provider.SecurityGroup {
		return c.tencentSecurityGroups(ctx, scope.Region, cursor, pageSize)
	}
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	var action string
	switch scope.Kind {
	case provider.Network:
		action = "DescribeVpcs"
	case provider.Subnet:
		action = "DescribeSubnets"
	}
	body, err := json.Marshal(struct{ Offset, Limit string }{strconv.Itoa(offset), strconv.Itoa(pageSize)})
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount *int
		VpcSet     []struct {
			VpcId, VpcName string
			TagSet         []*tag
		}
		SubnetSet []struct {
			SubnetId, SubnetName, VpcId, Zone string
			TagSet                            []*tag
		}
	}
	if err := c.tencent(ctx, "vpc", scope.Region, action, "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	var objects []provider.Object
	switch scope.Kind {
	case provider.Network:
		if result.VpcSet == nil {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range result.VpcSet {
			if !strings.HasPrefix(row.VpcId, "vpc-") {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			objects = append(objects, tagObject(row.VpcId, row.VpcName, "observed", row.TagSet))
		}
	case provider.Subnet:
		if result.SubnetSet == nil {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range result.SubnetSet {
			if !strings.HasPrefix(row.SubnetId, "subnet-") || !strings.HasPrefix(row.VpcId, "vpc-") || !resourceID.MatchString(row.VpcId) {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			item := tagObject(row.SubnetId, row.SubnetName, "observed", row.TagSet)
			item.Link(provider.Network, row.VpcId, "in_network")
			item.Link(provider.Zone, row.Zone, "in_zone")
			objects = append(objects, item)
		}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(objects), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	return provider.Page{Objects: objects, Next: next}, nil
}

func tencentNextOffset(offset int, total *int, count, limit int) (string, error) {
	if total == nil || *total < 0 || count > limit {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	end := offset + count
	if end > *total || (count == 0 && offset < *total) {
		return "", &provider.Failure{Kind: provider.ExpiredCursor}
	}
	if end == *total {
		return "", nil
	}
	return strconv.Itoa(end), nil
}

func (c *Client) tencentSecurityGroups(ctx context.Context, region, cursor string, limit int) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	body, err := json.Marshal(struct{ Offset, Limit string }{strconv.Itoa(offset), strconv.Itoa(limit)})
	if err != nil {
		return provider.Page{}, err
	}
	var result struct {
		TotalCount       *int
		SecurityGroupSet []struct {
			SecurityGroupId, SecurityGroupName, Region string
			TagSet                                     []*tag
		}
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSecurityGroups", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	rows := result.SecurityGroupSet
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(rows), limit)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		if row.Region != "" && row.Region != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !validSecurityGroupID(row.SecurityGroupId) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, tagObject(row.SecurityGroupId, row.SecurityGroupName, "observed", row.TagSet))
	}
	return page, nil
}
