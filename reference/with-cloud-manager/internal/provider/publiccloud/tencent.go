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
	req.Header.Set("X-TC-Region", region)
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
	offset := 0
	if cursor != "" {
		var err error
		offset, err = strconv.Atoi(cursor)
		if err != nil || offset < 1 || offset > 1_000_000 || strconv.Itoa(offset) != cursor {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}
	var result struct {
		TotalCount  *int
		InstanceSet []struct {
			InstanceId, InstanceName, InstanceState, ImageId string
			Placement                                        struct{ Zone string }
			VirtualPrivateCloud                              struct{ VpcId, SubnetId string }
			SystemDisk                                       struct{ DiskId string }
			DataDisks                                        []struct{ DiskId string }
			SecurityGroupIds                                 []string
			Tags                                             []*tag
		}
	}
	body, err := json.Marshal(struct{ Offset, Limit int }{offset, pageSize})
	if err != nil {
		return provider.Page{}, err
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeInstances", "2017-03-12", body, &result); err != nil {
		return provider.Page{}, err
	}
	if result.TotalCount == nil || *result.TotalCount < 0 || result.InstanceSet == nil || len(result.InstanceSet) > pageSize {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if (offset > 0 && offset > *result.TotalCount) || (len(result.InstanceSet) == 0 && offset < *result.TotalCount) || offset+len(result.InstanceSet) > *result.TotalCount {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	page := provider.Page{}
	if offset+len(result.InstanceSet) < *result.TotalCount {
		page.Next = strconv.Itoa(offset + len(result.InstanceSet))
	}
	for _, row := range result.InstanceSet {
		if !strings.HasPrefix(row.InstanceId, "ins-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := tagObject(row.InstanceId, row.InstanceName, row.InstanceState, row.Tags)
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
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}
