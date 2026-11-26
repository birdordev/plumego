package provider

import (
	"context"
	"regexp"
	"strings"
)

type CacheRename struct {
	ID          string
	Product     string
	CurrentName string
	Name        string
}

type CacheRenamer interface {
	PrepareCacheRename(context.Context, Scope, CacheRename) (func(context.Context) (string, error), error)
	ObserveCacheRename(context.Context, Scope, CacheRename) (ActionResult, error)
}

var cacheRenameName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{3,59}$`)
var cacheAssetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func ValidCacheRenameName(name string) bool {
	return cacheRenameName.MatchString(name)
}

func (change CacheRename) ValidFor(vendor string) bool {
	if !cacheAssetID.MatchString(change.ID) || !ValidCacheRenameName(change.Name) ||
		len(change.CurrentName) > 1024 || strings.ContainsRune(change.CurrentName, 0) || change.Name == change.CurrentName {
		return false
	}
	switch vendor {
	case TencentCVM:
		return change.Product == TencentRedis
	case AlibabaECS:
		return change.Product == AlibabaTair
	case HuaweiECS:
		return change.Product == HuaweiDCS
	default:
		return false
	}
}
