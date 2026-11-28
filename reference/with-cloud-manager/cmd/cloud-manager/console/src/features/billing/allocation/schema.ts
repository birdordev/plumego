import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { billingAmount, billingDigest, billingQuerySchema, billingRevision } from '../schema'

export const allocationQuerySchema = billingQuerySchema.extend({
  allocation_revision: billingQuerySchema.shape.revision,
}).refine((query) => !query.allocation_revision || Boolean(query.revision))

const allocationTotal = z.object({
  currency: z.string().regex(/^[A-Z]{3}$/),
  center: z.string().regex(/^[a-z0-9][a-z0-9_-]{0,63}$/),
  rows: z.number().int().nonnegative(),
  billed_cost: billingAmount,
  effective_cost: billingAmount.optional(),
  list_cost: billingAmount.optional(),
  list_minus_billed: billingAmount.optional(),
})

const legacyRevision = z.number().positive().refine(Number.isInteger)

export const allocationSchema = billingQuerySchema.omit({ revision: true }).extend({
  revision: legacyRevision,
  report_revision: legacyRevision,
  revision_text: billingRevision.optional(),
  report_revision_text: billingRevision.optional(),
  report_sha256: billingDigest,
  report_current: z.boolean(),
  algorithm: z.string(),
  policy_sha256: billingDigest,
  created_at: time,
  source_rows: z.number().int().nonnegative(),
  invalid_tag_rows: z.number().int().nonnegative(),
  current: z.boolean(),
  totals: z.array(allocationTotal).max(64 * 33),
}).refine((result) =>
  (Number.isSafeInteger(result.revision) || Boolean(result.revision_text)) &&
  (Number.isSafeInteger(result.report_revision) || Boolean(result.report_revision_text)))

export const allocationWriteQuerySchema = billingQuerySchema.extend({ revision: billingRevision })
export const allocationPreviewSchema = billingQuerySchema.omit({ revision: true }).extend({
  report_revision_text: billingRevision,
  report_current: z.boolean(),
  report_sha256: billingDigest,
  current_revision_text: z.union([z.literal('0'), billingRevision]),
  matching_revision_text: billingRevision.optional(),
  policy_sha256: billingDigest,
  source_rows: z.number().int().nonnegative(),
  invalid_tag_rows: z.number().int().nonnegative(),
  totals: z.array(allocationTotal).max(64 * 33),
})

export type AllocationWriteQuery = z.infer<typeof allocationWriteQuerySchema>
export type AllocationPreview = z.infer<typeof allocationPreviewSchema>
export type AllocationQuery = z.infer<typeof allocationQuerySchema>
export type Allocation = z.infer<typeof allocationSchema>
