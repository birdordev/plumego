import { z } from 'zod'
import { time } from '../../../shared/api/fields'

export const objectBucketSchema = z.object({
  product: z.string().min(1),
  location: z.string().min(1),
  storage_class: z.string().optional(),
  created_at: time,
})
