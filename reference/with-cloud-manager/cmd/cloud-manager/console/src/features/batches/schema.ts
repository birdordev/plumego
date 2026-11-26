import { z } from 'zod'
import { account, assetId, revision } from '../../shared/api/fields'

export const batchInputSchema = z.looseObject({
  items: z.array(z.looseObject({
    asset_id: assetId, account, region: z.string().min(1), action: z.string().min(1),
    ownership_revision: revision,
  })).min(1).max(25),
})

export const batchPreviewSchema = z.object({
  items: z.array(z.object({
    index: z.number().int().nonnegative(),
    reason: z.string(),
    ownership_revision: revision.optional(),
    volume_revision: revision.optional(),
    approval_required: z.boolean(),
  })).max(25),
  remaining: z.number().int(),
  can_submit: z.boolean(),
})

export const batchReceiptSchema = z.object({
  id: z.string().min(1),
  operations: z.array(z.object({
    id: z.string().min(1),
    asset_id: z.string(),
    action: z.string(),
    status: z.string(),
  })).max(25),
})

export type BatchPreview = z.infer<typeof batchPreviewSchema>
export type BatchReceipt = z.infer<typeof batchReceiptSchema>
