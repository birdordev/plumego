import { BucketSummary } from '../buckets/BucketSummary'
import { CacheSummary } from '../caches/CacheSummary'
import { ClusterSummary } from '../clusters/ClusterSummary'
import { DatabaseSummary } from '../databases/DatabaseSummary'
import { LoadBalancerSummary } from '../load-balancers/LoadBalancerSummary'
import { SnapshotSummary } from '../snapshots/SnapshotSummary'
import type { Asset } from '../types'

export function AssetSummary({ asset }: { asset: Asset }) {
  return <div>
    {asset.name || asset.id}
    {asset.name && asset.name !== asset.id ? <span className="minor">{asset.id}</span> : null}
    {asset.public_addresses?.length ? <span className="minor">{asset.public_addresses.join(' · ')}</span> : null}
    {asset.load_balancer ? <LoadBalancerSummary {...asset.load_balancer} /> : null}
    {asset.object_bucket ? <BucketSummary bucket={asset.object_bucket} /> : null}
    {asset.kubernetes_cluster ? <ClusterSummary cluster={asset.kubernetes_cluster} /> : null}
    {asset.cache_instance ? <CacheSummary cache={asset.cache_instance} /> : null}
    {asset.database_instance ? <DatabaseSummary database={asset.database_instance} /> : null}
    {asset.kind === 'snapshot' ? <SnapshotSummary parentID={asset.parent_id} snapshot={asset.snapshot} /> : null}
  </div>
}
