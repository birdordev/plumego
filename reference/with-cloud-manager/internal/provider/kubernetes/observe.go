package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"with-cloud-manager/internal/provider"
)

const parallelWorkloadWatches = 8

// ObservationBinding 将续读限定于同一端点、集群锚点、信任根和命名空间集合。
func (c *Client) ObservationBinding() [32]byte {
	namespaces := slices.Clone(c.config.Namespaces)
	slices.Sort(namespaces)
	fields := append([]string{c.endpoint, c.config.CA, c.config.IdentityNamespace, c.config.IdentityUID}, namespaces...)
	var encoded []byte
	for _, field := range fields {
		encoded = binary.AppendUvarint(encoded, uint64(len(field)))
		encoded = append(encoded, field...)
	}
	return sha256.Sum256(encoded)
}

type sourceKey struct{ namespace, kind string }

func (c *Client) ObserveWorkloads(parent context.Context, previous provider.WorkloadObservation) (provider.WorkloadObservation, error) {
	ctx, stop := context.WithTimeout(parent, 3*time.Minute)
	defer stop()
	if err := c.checkIdentity(ctx); err != nil {
		return provider.WorkloadObservation{}, err
	}
	sources := c.sources()
	base, err := snapshotBySource(sources, previous)
	if err != nil {
		return provider.WorkloadObservation{}, err
	}
	updated, err := c.observeSources(ctx, sources, base)
	if err != nil {
		return provider.WorkloadObservation{}, err
	}
	if err := c.checkIdentity(ctx); err != nil {
		return provider.WorkloadObservation{}, err
	}

	result := provider.WorkloadObservation{Items: make([]provider.ClusterWorkload, 0), Versions: make([]provider.WorkloadVersion, 0, len(updated))}
	for index, snapshot := range updated {
		if len(result.Items)+len(snapshot.items) > provider.MaxClusterWorkloads {
			return provider.WorkloadObservation{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		result.Items = append(result.Items, snapshot.items...)
		source := sources[index]
		result.Versions = append(result.Versions, provider.WorkloadVersion{Namespace: source.namespace, Kind: source.kind, Version: snapshot.version})
	}
	if err := provider.NormalizeClusterWorkloads(result.Items); err != nil {
		return provider.WorkloadObservation{}, err
	}
	return result, nil
}

func snapshotBySource(sources []workloadSource, previous provider.WorkloadObservation) (map[sourceKey]collectionSnapshot, error) {
	base := make(map[sourceKey]collectionSnapshot, len(sources))
	if len(previous.Versions) == 0 {
		return base, nil
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	allowed := make(map[sourceKey]bool, len(sources))
	for _, source := range sources {
		allowed[sourceKey{source.namespace, source.kind}] = true
	}
	for _, version := range previous.Versions {
		key := sourceKey{version.Namespace, version.Kind}
		if !allowed[key] || base[key].version != "" || !validResourceVersion(version.Version) {
			return nil, invalid
		}
		base[key] = collectionSnapshot{version: version.Version}
	}
	if len(base) != len(sources) {
		return nil, invalid
	}
	for _, item := range previous.Items {
		key := sourceKey{item.Namespace, item.Kind}
		snapshot, found := base[key]
		if !found || !item.Valid() {
			return nil, invalid
		}
		snapshot.items = append(snapshot.items, item)
		base[key] = snapshot
	}
	return base, nil
}

func (c *Client) observeSources(parent context.Context, sources []workloadSource, base map[sourceKey]collectionSnapshot) ([]collectionSnapshot, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	budget := newObservationBudget()
	results := make([]collectionSnapshot, len(sources))
	var next, items atomic.Int64
	var workers sync.WaitGroup
	for range min(parallelWorkloadWatches, len(sources)) {
		workers.Go(func() {
			for ctx.Err() == nil {
				index := int(next.Add(1) - 1)
				if index >= len(sources) {
					return
				}
				source := sources[index]
				initial := base[sourceKey{source.namespace, source.kind}]
				snapshot, err := c.observeSource(ctx, source, initial, budget)
				if err != nil {
					cancel(err)
					return
				}
				if items.Add(int64(len(snapshot.items))) > provider.MaxClusterWorkloads {
					cancel(&provider.Failure{Kind: provider.InvalidResponse})
					return
				}
				results[index] = snapshot
			}
		})
	}
	workers.Wait()
	return results, context.Cause(ctx)
}

func (c *Client) observeSource(ctx context.Context, source workloadSource, base collectionSnapshot, budget *observationBudget) (collectionSnapshot, error) {
	start := func() (collectionSnapshot, error) {
		fresh, err := c.list(ctx, source, budget)
		if err != nil {
			return collectionSnapshot{}, err
		}
		return c.watch(ctx, source, fresh, budget)
	}
	var result collectionSnapshot
	var err error
	if base.version == "" {
		result, err = start()
	} else {
		result, err = c.watch(ctx, source, base, budget)
	}
	if provider.IsFailure(err, provider.ExpiredCursor) {
		return start()
	}
	return result, err
}
