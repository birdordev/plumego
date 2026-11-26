import { z } from 'zod'
import { revision, time } from '../../shared/api/fields'

const period = z.string().regex(/^\d{4}-(0[1-9]|1[0-2])$/)
const source = z.enum(['native', 'focus-1.0'])
export const billingDigest = z.string().regex(/^[0-9a-f]{64}$/)
export const billingAmount = z.string().regex(/^-?\d+(\.\d{1,12})?$/)

export const billingQuerySchema = z.object({
  account: z.string().trim().min(1).max(128),
  period,
  source,
  revision: z.string().regex(/^[1-9][0-9]{0,18}$/).optional(),
})

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

export type BillingQuery = z.infer<typeof billingQuerySchema>
export type BillingReport = z.infer<typeof billingReportSchema>
