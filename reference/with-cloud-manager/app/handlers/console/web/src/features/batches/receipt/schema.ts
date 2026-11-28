import { z } from 'zod'

export const batchReceiptSchema = z.object({
  id: z.string().min(1),
  operations: z.array(z.object({
    id: z.string().min(1),
    asset_id: z.string(),
    action: z.string(),
    status: z.string(),
  })).max(25),
})

export type BatchReceipt = z.infer<typeof batchReceiptSchema>
