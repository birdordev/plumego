import { z } from 'zod'
import { assetId } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'
import { objectBucketSchema } from './buckets/schema'
import { cacheSchema } from './caches/schema'
import { clusterSchema } from './clusters/schema'
import { databaseSchema } from './databases/schema'
import { loadBalancerSummarySchema } from './load-balancers/summary-schema'
import { snapshotSummarySchema } from './snapshots/schema'
import { volumeSchema } from './volumes/schema'

export const assetSchema = z.object({
  id: assetId,
  name: z.string(),
  kind: z.string(),
  account: z.string(),
  provider: z.string(),
  region: z.string(),
  status: z.string(),
  inventory_state: z.string(),
  ownership_revision: z.number().int().nonnegative(),
  parent_id: z.string().optional(),
  public_addresses: z.array(z.string()).optional(),
  load_balancer: loadBalancerSummarySchema.optional(),
  database_instance: databaseSchema.optional(),
  cache_instance: cacheSchema.optional(),
  kubernetes_cluster: clusterSchema.optional(),
  object_bucket: objectBucketSchema.optional(),
  snapshot: snapshotSummarySchema.optional(),
  volume: volumeSchema.optional(),
})

export const assetPageSchema = pageSchema(assetSchema)
