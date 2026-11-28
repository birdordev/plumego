package publiccloud

import (
	"context"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaRecoveryProtection struct {
	diskKey   string
	whitelist []string
}

func (c *Client) readAlibabaRecoveryProtection(ctx context.Context, region, id string) (alibabaRecoveryProtection, error) {
	result := alibabaRecoveryProtection{}
	params := url.Values{"DBInstanceId": {id}}
	var endpoints struct {
		DBInstanceNetInfos struct {
			DBInstanceNetInfo []struct{ DBInstanceId, IPType string }
		}
	}
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeDBInstanceNetInfo", params, &endpoints); err != nil {
		return result, err
	}
	rows := endpoints.DBInstanceNetInfos.DBInstanceNetInfo
	if len(rows) == 0 || len(rows) > 64 {
		return result, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, row := range rows {
		if row.DBInstanceId != id || row.IPType != "Private" {
			return result, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	var bindings struct {
		Items struct {
			EcsSecurityGroupRelation []struct{ SecurityGroupId string }
		}
	}
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeSecurityGroupConfiguration", params, &bindings); err != nil {
		return result, err
	}
	if groups := bindings.Items.EcsSecurityGroupRelation; groups == nil || len(groups) != 0 {
		return result, &provider.Failure{Kind: provider.InvalidResponse}
	}
	whitelist, err := c.readAlibabaRecoveryWhitelist(ctx, region, params)
	if err != nil {
		return result, err
	}
	result.whitelist = whitelist
	var tde struct{ TDEStatus string }
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeDBInstanceTDE", params, &tde); err != nil {
		return result, err
	}
	if tde.TDEStatus != "Disabled" {
		return result, &provider.Failure{Kind: provider.InvalidResponse}
	}
	result.diskKey, err = c.readAlibabaRecoveryDiskKey(ctx, region, params)
	return result, err
}

func (c *Client) readAlibabaRecoveryDiskKey(ctx context.Context, region string, params url.Values) (string, error) {
	var result struct {
		EncryptionKey       *string
		EncryptionKeyList   []struct{ EncryptionKey, EncryptionKeyStatus, KeyType, UsedBy string }
		EncryptionKeyStatus string
	}
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeDBInstanceEncryptionKey", params, &result); err != nil {
		return "", err
	}
	if len(result.EncryptionKeyList) == 0 && (result.EncryptionKey == nil || *result.EncryptionKey == "") &&
		(result.EncryptionKeyList != nil || (result.EncryptionKey != nil && result.EncryptionKeyStatus == "Disabled")) &&
		(result.EncryptionKeyStatus == "" || result.EncryptionKeyStatus == "Disabled") {
		return "", nil
	}
	if len(result.EncryptionKeyList) != 1 {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	key := result.EncryptionKeyList[0]
	if key.UsedBy != "DiskEncryption" || key.EncryptionKeyStatus != "Enabled" || key.KeyType != "ServiceKey" ||
		(result.EncryptionKeyStatus != "" && result.EncryptionKeyStatus != "Enabled") ||
		!resourceID.MatchString(key.EncryptionKey) || (result.EncryptionKey != nil && *result.EncryptionKey != key.EncryptionKey) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return key.EncryptionKey, nil
}

func (c *Client) readAlibabaRecoveryWhitelist(ctx context.Context, region string, params url.Values) ([]string, error) {
	var result struct {
		Items struct {
			DBInstanceIPArray []struct{ SecurityIPList *string }
		}
	}
	if err := c.alibabaRecoveryRPC(ctx, region, "DescribeDBInstanceIPArrayList", params, &result); err != nil {
		return nil, err
	}
	groups := result.Items.DBInstanceIPArray
	if groups == nil || len(groups) > 100 {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var whitelist []string
	for _, group := range groups {
		if group.SecurityIPList == nil {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if *group.SecurityIPList == "" {
			continue
		}
		for entry := range strings.SplitSeq(*group.SecurityIPList, ",") {
			prefix, ok := privateDatabaseRecoveryPrefix(entry)
			if !ok || len(whitelist) >= 1024 {
				return nil, &provider.Failure{Kind: provider.InvalidResponse}
			}
			whitelist = append(whitelist, prefix.String())
		}
	}
	slices.Sort(whitelist)
	return slices.Compact(whitelist), nil
}

func privateDatabaseRecoveryPrefix(entry string) (netip.Prefix, bool) {
	entry = strings.TrimSpace(entry)
	prefix, err := netip.ParsePrefix(entry)
	if err != nil {
		address, err := netip.ParseAddr(entry)
		if err != nil || address.Zone() != "" {
			return netip.Prefix{}, false
		}
		prefix = netip.PrefixFrom(address, address.BitLen())
	}
	prefix = prefix.Masked()
	for _, block := range privateDatabaseRecoveryBlocks {
		if block.Bits() <= prefix.Bits() && block.Contains(prefix.Addr()) {
			return prefix, true
		}
	}
	return netip.Prefix{}, false
}

var privateDatabaseRecoveryBlocks = [...]netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("::1/128"),
}
