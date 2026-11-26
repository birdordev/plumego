import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { billingAmount, billingDigest, billingQuerySchema } from '../schema'

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

export const allocationSchema = billingQuerySchema.omit({ revision: true }).extend({
  revision,
  report_revision: revision,
  report_sha256: billingDigest,
  report_current: z.boolean(),
  algorithm: z.string(),
  policy_sha256: billingDigest,
  created_at: time,
  source_rows: z.number().int().nonnegative(),
  invalid_tag_rows: z.number().int().nonnegative(),
  current: z.boolean(),
  totals: z.array(allocationTotal).max(64 * 33),
})

export type AllocationQuery = z.infer<typeof allocationQuerySchema>
export type Allocation = z.infer<typeof allocationSchema>
