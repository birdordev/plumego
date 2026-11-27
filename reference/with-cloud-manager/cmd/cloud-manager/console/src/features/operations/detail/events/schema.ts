import { z } from 'zod'
import { time } from '../../../../shared/api/fields'

export const operationEventsSchema = z.object({
  events: z.array(z.object({ recorded_at: time, status: z.string() })).max(100),
  next_after: z.string().regex(/^\d+$/),
  has_more: z.boolean(),
})

export type OperationEvents = z.infer<typeof operationEventsSchema>
