import { z } from 'zod'

export const databaseSchema = z.object({
  product: z.string().min(1),
  engine: z.string().min(1),
  version: z.string().optional(),
})
