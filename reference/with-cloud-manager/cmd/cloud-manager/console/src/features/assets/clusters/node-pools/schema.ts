import { z } from 'zod'
import { snapshotMetaSchema } from '../snapshot'

const count = z.number().int().min(0).max(1_000_000)
const nodePoolSchema = z.object({
  id: z.string().min(1).max(128),
  name: z.string().min(1).max(512),
  type: z.string().optional(),
  status: z.string().optional(),
  current_nodes: count.optional(),
  desired_nodes: count.optional(),
  min_nodes: count.optional(),
  max_nodes: count.optional(),
  autoscaling: z.boolean().optional(),
})

export const nodePoolPageSchema = snapshotMetaSchema(1000).extend({
  node_pools: z.array(nodePoolSchema).max(100),
}).transform(({ node_pools, ...page }) => ({ ...page, items: node_pools }))

export type NodePool = z.infer<typeof nodePoolSchema>
