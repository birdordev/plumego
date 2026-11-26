import { z } from 'zod'
import { revision, time } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'

export const alertSchema = z.object({
  id: z.string(),
  opened_at: time,
  rule: z.string(),
  region: z.string().optional(),
  account: z.string(),
  subject_id: z.string(),
  resource_kind: z.string(),
  status: z.string(),
})

export const alertDetailSchema = alertSchema.extend({
  source_status: z.string(),
  revision,
  acknowledged_at: time.optional(),
  acknowledged_by: z.string().optional(),
  resolved_at: time.optional(),
  budget_scope: z.object({
    period: z.string(), source: z.enum(['native', 'focus-1.0']), currency: z.string(), basis: z.enum(['billed', 'effective']),
  }).optional(),
})

export const alertPageSchema = pageSchema(alertSchema)
