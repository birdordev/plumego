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
	name, _, _ := strings.Cut(c.ID, "@")
	return !strings.Contains(name, ".") && ValidBucketAssetID(c.ID) && c.Current.Valid() && c.Current != c.Target &&
		(c.Target == BucketVersioningEnabled || (c.Current == BucketVersioningEnabled && c.Target == BucketVersioningSuspended))
}

type BucketVersioningOperator interface {
	BucketVersioningReader
	PrepareBucketVersioningChange(context.Context, Scope, BucketVersioningChange) (func(context.Context) (string, error), error)
}
