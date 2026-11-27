import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { grantSchema } from '../schema'

export const grantDetailSchema = grantSchema.extend({
  account: z.string(),
  region: z.string(),
  provider: z.string(),
  revision,
  reviewer: z.string().optional(),
  key_fingerprint: z.string().optional(),
  provider_request_id: z.string().optional(),
})

export type GrantDetail = z.infer<typeof grantDetailSchema>

export const serialReceiptSchema = z.object({
  ssh: z.object({
    host: z.string().regex(/^[a-zA-Z0-9.-]+$/),
    username: z.string().regex(/^[a-zA-Z0-9_.-]+$/),
    port: z.number().int().min(1).max(65535),
    valid_until: time,
  }),
})
