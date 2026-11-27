import { z } from 'zod'
import { time } from '../../shared/api/fields'

export const operationSchema = z.object({
  id: z.string(),
  action: z.string(),
  status: z.string(),
  queue_issue_reason: z.string().optional(),
  asset_id: z.string(),
  created_at: time,
})

export type Operation = z.infer<typeof operationSchema>
