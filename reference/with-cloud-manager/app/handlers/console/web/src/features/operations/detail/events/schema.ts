import { z } from 'zod'
import { time } from '../../../../shared/api/fields'

export const operationEventSchema = z.object({
  sequence: z.string().regex(/^[1-9][0-9]{0,18}$/),
  recorded_at: time,
  status: z.string(),
})

export const operationEventsSchema = z.object({
  events: z.array(operationEventSchema).max(100),
  next_after: z.string().regex(/^\d+$/),
  has_more: z.boolean(),
})

export type OperationEvents = z.infer<typeof operationEventsSchema>
export type OperationEvent = z.infer<typeof operationEventSchema>
