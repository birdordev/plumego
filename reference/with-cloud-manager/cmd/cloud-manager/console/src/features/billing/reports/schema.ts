import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'

import { billingAmount, billingDigest, period, source } from '../schema'

export const billingReportSchema = z.object({
  provider: z.string(),
  account: z.string(),
  period,
  source,
  revision,
  sha256: billingDigest,
  imported_at: time,
  rows: z.number().int().nonnegative(),
  current: z.boolean(),
  parts: z.array(z.object({
    number: z.number().int().positive(), sha256: billingDigest, bytes: z.number().int().positive(), rows: z.number().int().nonnegative(),
  })).min(1).max(32),
  totals: z.array(z.object({
    currency: z.string().regex(/^[A-Z]{3}$/),
    billed_cost: billingAmount,
    effective_cost: billingAmount.optional(),
    list_cost: billingAmount.optional(),
    expected_billed_cost: billingAmount.optional(),
    variance: billingAmount.optional(),
  })).max(64),
})

export type BillingReport = z.infer<typeof billingReportSchema>
