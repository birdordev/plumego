package publiccloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) alibaba(ctx context.Context, host, action, version string, query url.Values, dst any) error {
	endpoint := url.URL{Scheme: "https", Host: host, Path: "/", RawQuery: strings.ReplaceAll(query.Encode(), "+", "%20")}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	headers := map[string]string{
		"host": host, "x-acs-action": action, "x-acs-version": version, "x-acs-content-sha256": sha(nil),
		"x-acs-date": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "x-acs-signature-nonce": rand.Text(),
	}
	if c.profile.SecurityToken != "" {
		headers["x-acs-security-token"] = c.profile.SecurityToken
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
	canonical := "POST\n/\n" + endpoint.RawQuery + "\n" + flattened.String() + "\n" + signed + "\n" + headers["x-acs-content-sha256"]
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
		Instances       struct {
			Instance []struct {
				InstanceId, InstanceName, Status, RegionId, ZoneId, ImageId string
				VpcAttributes                                               struct{ VpcId, VSwitchId string }
				SecurityGroupIds                                            struct{ SecurityGroupId []string }
				Tags                                                        struct {
					Tag []struct{ TagKey, TagValue string }
				}
			}
		}
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
		var tags []*tag
		if row.Tags.Tag != nil {
			tags = make([]*tag, 0, len(row.Tags.Tag))
			for _, value := range row.Tags.Tag {
				tags = append(tags, &tag{Key: value.TagKey, Value: value.TagValue})
			}
		}
		item := tagObject(row.InstanceId, row.InstanceName, row.Status, tags)
		item.Link(provider.Zone, row.ZoneId, "in_zone")
		item.Link(provider.Network, row.VpcAttributes.VpcId, "in_network")
		item.Link(provider.Subnet, row.VpcAttributes.VSwitchId, "in_subnet")
		item.Link(provider.Image, row.ImageId, "uses_image")
		for _, group := range row.SecurityGroupIds.SecurityGroupId {
			item.Link(provider.SecurityGroup, group, "uses_security_group")
		}
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}
