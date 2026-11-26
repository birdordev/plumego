package provider

import (
	"context"
	"regexp"
	"strings"
)

type DatabaseRename struct {
	ID          string
	Product     string
	CurrentName string
	Name        string
}

type DatabaseRenamer interface {
	PrepareDatabaseRename(context.Context, Scope, DatabaseRename) (func(context.Context) (string, error), error)
	ObserveDatabaseRename(context.Context, Scope, DatabaseRename) (ActionResult, error)
}

var databaseRenameName = regexp.MustCompile(`^[A-Za-z\p{Han}][A-Za-z0-9_\p{Han}]{3,58}$`)

func ValidDatabaseRenameName(name string) bool {
	return len(name) <= 64 && databaseRenameName.MatchString(name)
}

func (change DatabaseRename) ValidFor(vendor string) bool {
	if !databaseAssetID.MatchString(change.ID) || !ValidDatabaseRenameName(change.Name) ||
		len(change.CurrentName) > 1024 || strings.ContainsRune(change.CurrentName, 0) || change.Name == change.CurrentName {
		return false
	}
	switch vendor {
	case TencentCVM:
		product := TencentDatabaseProduct(change.ID)
		return product != "" && product == change.Product
	case AlibabaECS:
		return change.Product == AlibabaRDS
	case HuaweiECS:
		return change.Product == HuaweiRDS
	}
	return false
}

var databaseAssetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
