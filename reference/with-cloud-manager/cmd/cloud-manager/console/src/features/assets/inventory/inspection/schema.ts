import { z } from 'zod'
import { time } from '../../../../shared/api/fields'
import { pageSchema } from '../../../../shared/api/page'
import { assetSchema } from '../../schema'
import { volumeSchema } from '../../volumes/schema'

const assetChange = z.object({
  account: z.string(), region: z.string(), kind: z.string(), asset_id: z.string(), action: z.string(),
  name: z.string(), status: z.string(), occurred_at: time,
  previous_name: z.string().optional(), previous_status: z.string().optional(),
  volume: volumeSchema.optional(), previous_volume: volumeSchema.optional(),
})

const relation = z.object({ kind: z.string(), id: z.string(), relation: z.string(), name: z.string() })

export const assetHistoryPage = pageSchema(assetChange)
export const assetTopologyPage = z.object({
  nodes: z.array(z.object({ asset: assetSchema, links: z.array(relation) })), next: z.string().optional(),
})

export type AssetHistoryPage = z.infer<typeof assetHistoryPage>
export type AssetTopologyPage = z.infer<typeof assetTopologyPage>
