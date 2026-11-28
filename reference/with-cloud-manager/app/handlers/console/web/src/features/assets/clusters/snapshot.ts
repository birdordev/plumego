import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { request } from '../../session/client'
import type { Asset } from '../types'

export type ClusterAsset = Pick<Asset, 'id' | 'account' | 'region' | 'name'>

export type ClusterSnapshot<T> = {
  observed_at: string
  snapshot: string
  total: number
  next_offset?: number
  items: T[]
}

type SnapshotCursor = Pick<ClusterSnapshot<unknown>, 'snapshot' | 'next_offset'>
export type ClusterSnapshotReader<T> = (
  base: string, cluster: ClusterAsset, signal: AbortSignal, previous?: SnapshotCursor,
) => Promise<ClusterSnapshot<T>>

export function snapshotMetaSchema(maxItems: number) {
  return z.object({
    observed_at: time,
    snapshot: z.uuid(),
    total: z.number().int().min(0).max(maxItems),
    next_offset: z.number().int().min(1).max(maxItems - 1).optional(),
  })
}

export function clusterSnapshotReader<T>(
  resource: 'node-pools' | 'workloads',
  schema: z.ZodType<ClusterSnapshot<T>>,
): ClusterSnapshotReader<T> {
  return (base, cluster, signal, previous) => {
    const query = new URLSearchParams({ account: cluster.account, region: cluster.region })
    if (previous?.next_offset) {
      query.set('offset', String(previous.next_offset))
      query.set('snapshot', previous.snapshot)
    }
    const path = base + '/assets/kubernetes-clusters/' + encodeURIComponent(cluster.id) + '/' + resource + '?' + query
    return request(path, { signal }, schema)
  }
}
