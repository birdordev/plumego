import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { operationSchema } from '../schema'
import { operationIntents } from './intent-schema'

export const operationDetailSchema = operationSchema.extend({
  account: z.string(),
  region: z.string(),
  provider_request_id: z.string().optional(),
  catalog_template: z.string().optional(),
  catalog_version: z.number().int().optional(),
  ...operationIntents,
})

export type OperationDetail = z.infer<typeof operationDetailSchema>

export const operationEventsSchema = z.object({
  events: z.array(z.object({ recorded_at: time, status: z.string() })).max(100),
  next_after: z.string().regex(/^\d+$/),
  has_more: z.boolean(),
})

export type OperationEvents = z.infer<typeof operationEventsSchema>
