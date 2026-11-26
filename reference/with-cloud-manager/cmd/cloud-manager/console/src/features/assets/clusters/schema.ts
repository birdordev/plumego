import { z } from 'zod'

export const clusterSchema = z.object({
  product: z.string().min(1),
  cluster_type: z.string().min(1),
  version: z.string().optional(),
  node_count: z.number().int().nonnegative().max(1_000_000).optional(),
})
