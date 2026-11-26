package publiccloud

import (
	"context"
	"encoding/xml"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const (
	bucketPageSize         = 100
	bucketObservationPages = 100
)

type bucketRow struct {
	Name, Location, Region, CreationDate, StorageClass, BucketType string
}

type bucketList struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Owners  []struct {
		IDs []string `xml:"ID"`
	} `xml:"Owner"`
	Collections []struct {
		Items []bucketRow `xml:"Bucket"`
	} `xml:"Buckets"`
	Markers     []string `xml:"Marker"`
	NextMarkers []string `xml:"NextMarker"`
	Truncated   []string `xml:"IsTruncated"`
}

var bucketMarker = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,255}$`)

func (c *Client) discoverBuckets(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Region != provider.ObjectBucketGlobalScope {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if cursor != "" && !bucketMarker.MatchString(cursor) {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}

	var result bucketList
	var owner, product string
	limit := bucketPageSize
	if c.name == provider.HuaweiECS {
		if cursor != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		owner, product, limit = c.profile.AccountID, provider.HuaweiOBS, provider.MaxPageObjects
		if err := c.listOBSBuckets(ctx, &result); err != nil {
			return provider.Page{}, err
		}
	} else {
		query := url.Values{"max-keys": {strconv.Itoa(bucketPageSize)}}
		if cursor != "" {
			query.Set("marker", cursor)
		}
		switch c.name {
		case provider.TencentCVM:
			owner, product = "qcs::cam::uin/"+c.identity+":uin/"+c.identity, provider.TencentCOS
			if err := c.listCOSBuckets(ctx, query, &result); err != nil {
				return provider.Page{}, err
			}
		case provider.AlibabaECS:
			owner, product = c.identity, provider.AlibabaOSS
			if err := c.listOSSBuckets(ctx, query, &result); err != nil {
				return provider.Page{}, err
			}
		default:
			return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
		}
	}

	if len(result.Owners) != 1 || len(result.Owners[0].IDs) != 1 || result.Owners[0].IDs[0] != owner {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if len(result.Collections) != 1 || len(result.Collections[0].Items) > limit ||
		len(result.Markers) > 1 || (len(result.Markers) == 1 && result.Markers[0] != "" && result.Markers[0] != cursor) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	rows := result.Collections[0].Items
	next, err := result.nextCursor(product, cursor, len(rows), limit)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows)), Next: next}
	names := make(map[string]bool, len(rows))
	for _, row := range rows {
		item, err := row.asset(product)
		if err != nil || names[item.Name] {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		names[item.Name] = true
		page.Objects = append(page.Objects, item)
	}
	return page, page.Validate(cursor)
}

func (r bucketList) nextCursor(product, cursor string, count, limit int) (string, error) {
	if len(r.NextMarkers) > 1 || len(r.Truncated) > 1 {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	var next string
	if len(r.NextMarkers) == 1 {
		next = r.NextMarkers[0]
	}
	if len(r.Truncated) == 0 {
		if next == "" && (product == provider.HuaweiOBS || (product == provider.AlibabaOSS && count < limit)) {
			return "", nil
		}
	} else if r.Truncated[0] == "false" && next == "" {
		return "", nil
	} else if r.Truncated[0] == "true" && product != provider.HuaweiOBS &&
		count > 0 && bucketMarker.MatchString(next) && next != cursor {
		return next, nil
	}
	return "", &provider.Failure{Kind: provider.InvalidResponse}
}

func (row bucketRow) asset(product string) (provider.Object, error) {
	if (product == provider.HuaweiOBS && row.BucketType != "" && row.BucketType != "OBJECT") ||
		(product == provider.TencentCOS && row.BucketType != "" && row.BucketType != "cos") {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	location := row.Location
	if product == provider.AlibabaOSS {
		// OSS 较旧响应只提供带 oss 前缀的 Location。
		location = strings.TrimPrefix(location, "oss-")
		if row.Region != "" {
			if location != "" && row.Region != location {
				return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			location = row.Region
		}
	}
	created, err := time.Parse(time.RFC3339Nano, row.CreationDate)
	if err != nil {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	item := provider.Object{
		ID: provider.BucketAssetID(row.Name, created), Name: row.Name, Status: "observed",
		Bucket: &provider.BucketDetails{Product: product, Location: location, StorageClass: row.StorageClass, CreatedAt: created.UTC()},
	}
	if !item.ValidBucket() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func (c *Client) observeBucket(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !provider.ValidBucketAssetID(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	var cursor string
	cursors := make(map[string]bool)
	for range bucketObservationPages {
		page, err := c.discoverBuckets(ctx, scope, cursor)
		if err != nil {
			return provider.Object{}, err
		}
		for _, item := range page.Objects {
			if item.ID == id {
				return item, nil
			}
		}
		if page.Next == "" {
			if !c.profile.BucketListAccountWide {
				return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
			}
			return provider.Object{}, &provider.Failure{Kind: provider.Absent}
		}
		if cursors[page.Next] {
			break
		}
		cursors[page.Next] = true
		cursor = page.Next
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}
