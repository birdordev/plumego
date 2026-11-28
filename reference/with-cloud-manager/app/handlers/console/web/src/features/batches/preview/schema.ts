import { z } from 'zod'
import { revision } from '../../../shared/api/fields'

export const batchPreviewSchema = z.object({
  items: z.array(z.object({
    index: z.number().int().nonnegative(),
    reason: z.string(),
    ownership_revision: revision.optional(),
    volume_revision: revision.optional(),
    approval_required: z.boolean(),
    ticket_required: z.boolean().optional(),
  })).max(25),
  remaining: z.number().int(),
  can_submit: z.boolean(),
})

export type BatchPreview = z.infer<typeof batchPreviewSchema>
