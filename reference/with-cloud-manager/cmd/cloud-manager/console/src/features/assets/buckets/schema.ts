import { z } from 'zod'
import { time } from '../../../shared/api/fields'

export const objectBucketSchema = z.object({
  product: z.string().min(1),
  location: z.string().min(1),
  storage_class: z.string().optional(),
  created_at: time,
})

export const bucketVersioningSchema = z.object({
  state: z.enum(['disabled', 'enabled', 'suspended']),
  observed_at: time,
})

export const bucketEncryptionSchema = z.object({
  state: z.enum(['unconfigured', 'aes256', 'sm4', 'kms']),
  observed_at: time,
})
