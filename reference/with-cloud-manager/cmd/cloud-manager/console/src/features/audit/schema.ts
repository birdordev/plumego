import { z } from 'zod'
import { time } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'

export const auditEventSchema = z.object({
  id: z.string(),
  category: z.string(),
  occurred_at: time,
  subject_id: z.string(),
  related_project: z.string().optional(),
  actor: z.string().optional(),
  action: z.string(),
  outcome: z.string().optional(),
})

export const auditPageSchema = pageSchema(auditEventSchema)
