package provider

import (
	"context"
	"strings"
)

type BucketVersioningChange struct {
	ID, Location string
	Current      BucketVersioning
	Target       BucketVersioning
}

func (c BucketVersioningChange) Valid() bool {
	return ValidBucketSettingAssetID(c.ID) && c.Current.Valid() && c.Current != c.Target &&
		(c.Target == BucketVersioningEnabled || (c.Current == BucketVersioningEnabled && c.Target == BucketVersioningSuspended))
}

func ValidBucketSettingAssetID(id string) bool {
	name, _, _ := strings.Cut(id, "@")
	return ValidBucketAssetID(id) && !strings.Contains(name, ".")
}

type BucketVersioningOperator interface {
	BucketVersioningReader
	PrepareBucketVersioningChange(context.Context, Scope, BucketVersioningChange) (func(context.Context) (string, error), error)
}

type BucketEncryptionOperator interface {
	BucketEncryptionReader
	PrepareBucketEncryptionEnable(context.Context, Scope, string, string) (func(context.Context) (string, error), error)
}
