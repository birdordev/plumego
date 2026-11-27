package publiccloud

import (
	"context"
	"encoding/xml"
	"net/http"

	"with-cloud-manager/internal/provider"
)

const maxBucketEncryptionXML = 8 << 10

type bucketEncryptionDefault struct {
	Algorithms []struct {
		Value string     `xml:",chardata"`
		Other []struct{} `xml:",any"`
	} `xml:"SSEAlgorithm"`
}

func (c *Client) ReadBucketEncryption(ctx context.Context, scope provider.Scope, id, location string) (provider.BucketEncryption, error) {
	return readBucketSetting(ctx, c, scope, id, location, "encryption", "GetEncryptionConfiguration", c.getBucketEncryption)
}

func (c *Client) getBucketEncryption(req *http.Request) (provider.BucketEncryption, error) {
	res, err := c.openObjectStorageXML(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return c.parseBucketEncryptionError(res)
	}
	var defaults []bucketEncryptionDefault
	if c.name == provider.AlibabaECS {
		var result struct {
			XMLName  xml.Name                  `xml:"ServerSideEncryptionRule"`
			Defaults []bucketEncryptionDefault `xml:"ApplyServerSideEncryptionByDefault"`
		}
		if err := decodeObjectStorageXML(res.Body, &result, maxBucketEncryptionXML, false); err != nil {
			return "", err
		}
		defaults = result.Defaults
	} else {
		var result struct {
			XMLName xml.Name `xml:"ServerSideEncryptionConfiguration"`
			Rules   []struct {
				Defaults []bucketEncryptionDefault `xml:"ApplyServerSideEncryptionByDefault"`
			} `xml:"Rule"`
		}
		if err := decodeObjectStorageXML(res.Body, &result, maxBucketEncryptionXML, false); err != nil {
			return "", err
		}
		if len(result.Rules) == 1 {
			defaults = result.Rules[0].Defaults
		}
	}
	return c.bucketEncryptionState(defaults)
}

func (c *Client) parseBucketEncryptionError(res *http.Response) (provider.BucketEncryption, error) {
	status, absent := http.StatusNotFound, "NoSuchEncryptionConfiguration"
	if c.name == provider.AlibabaECS {
		status, absent = http.StatusBadRequest, "NoSuchServerSideEncryptionRule"
	}
	if res.StatusCode != status {
		return "", cloudHTTPFailure(res)
	}
	var result struct {
		XMLName xml.Name `xml:"Error"`
		Codes   []string `xml:"Code"`
	}
	if err := decodeObjectStorageXML(res.Body, &result, maxBucketEncryptionXML, false); err != nil {
		return "", err
	}
	if len(result.Codes) == 1 && result.Codes[0] == absent {
		return provider.BucketEncryptionUnconfigured, nil
	}
	return "", &provider.Failure{Kind: provider.OutcomeUnknown}
}

func (c *Client) bucketEncryptionState(defaults []bucketEncryptionDefault) (provider.BucketEncryption, error) {
	if len(defaults) == 1 && len(defaults[0].Algorithms) == 1 && len(defaults[0].Algorithms[0].Other) == 0 {
		switch defaults[0].Algorithms[0].Value {
		case "AES256":
			return provider.BucketEncryptionAES256, nil
		case "SM4":
			if c.name != provider.HuaweiECS {
				return provider.BucketEncryptionSM4, nil
			}
		case "KMS":
			if c.name != provider.HuaweiECS {
				return provider.BucketEncryptionKMS, nil
			}
		case "kms":
			if c.name == provider.HuaweiECS {
				return provider.BucketEncryptionKMS, nil
			}
		}
	}
	return "", &provider.Failure{Kind: provider.InvalidResponse}
}
