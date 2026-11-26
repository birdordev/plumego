import { z } from 'zod'

export const cacheSchema = z.object({
  product: z.string().min(1),
  engine: z.enum(['redis', 'memcached', 'ckv']),
  version: z.string().optional(),
  capacity_mib: z.number().positive().optional(),
})
