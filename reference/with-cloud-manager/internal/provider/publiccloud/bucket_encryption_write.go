package publiccloud

import (
	"context"
	"net/http"

	"with-cloud-manager/internal/provider"
)

const aes256EncryptionDefault = "<ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault>"

func (c *Client) PrepareBucketEncryptionEnable(ctx context.Context, scope provider.Scope, id, location string) (func(context.Context) (string, error), error) {
	if !c.profile.AllowBucketEncryptionEnable {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	body := "<ServerSideEncryptionConfiguration><Rule>" + aes256EncryptionDefault + "</Rule></ServerSideEncryptionConfiguration>"
	status := http.StatusOK
	switch c.name {
	case provider.TencentCVM:
		status = http.StatusNoContent
	case provider.AlibabaECS:
		body = "<ServerSideEncryptionRule>" + aes256EncryptionDefault + "</ServerSideEncryptionRule>"
	}
	return prepareBucketSetting(ctx, c, scope, id, location, provider.BucketEncryptionUnconfigured, c.ReadBucketEncryption,
		"encryption", "PutEncryptionConfiguration", []byte(body), status)
}
