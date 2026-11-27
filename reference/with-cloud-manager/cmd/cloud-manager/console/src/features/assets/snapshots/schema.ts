import { z } from 'zod'

export const snapshotSummarySchema = z.object({
  class: z.string().optional(),
  size_gib: z.number().int().nonnegative().optional(),
})

export type SnapshotSummaryData = z.infer<typeof snapshotSummarySchema>
