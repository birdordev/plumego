package publiccloud

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"

	"with-cloud-manager/internal/provider"
)

type bucketVersioningXML struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Text    string   `xml:",chardata"`
	Status  []struct {
		Value string     `xml:",chardata"`
		Other []struct{} `xml:",any"`
	} `xml:"Status"`
	Other []struct{} `xml:",any"`
}

func (c *Client) ReadBucketVersioning(ctx context.Context, scope provider.Scope, id, location string) (provider.BucketVersioning, error) {
	return readBucketSetting(ctx, c, scope, id, location, "versioning", "GetBucketVersioning", c.getBucketVersioning)
}

func (c *Client) getBucketVersioning(req *http.Request) (provider.BucketVersioning, error) {
	var result bucketVersioningXML
	if err := c.readObjectStorageXML(req, &result, 4096, c.name == provider.HuaweiECS); err != nil {
		return "", err
	}
	return result.state()
}

func (result bucketVersioningXML) state() (provider.BucketVersioning, error) {
	if len(result.Other) == 0 && strings.TrimSpace(result.Text) == "" {
		if len(result.Status) == 0 {
			return provider.BucketVersioningDisabled, nil
		}
		if len(result.Status) == 1 && len(result.Status[0].Other) == 0 {
			switch result.Status[0].Value {
			case "Enabled":
				return provider.BucketVersioningEnabled, nil
			case "Suspended":
				return provider.BucketVersioningSuspended, nil
			}
		}
	}
	return "", &provider.Failure{Kind: provider.InvalidResponse}
}
