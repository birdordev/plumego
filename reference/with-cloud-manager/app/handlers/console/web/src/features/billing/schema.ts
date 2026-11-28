import { z } from 'zod'

export const period = z.string().regex(/^\d{4}-(0[1-9]|1[0-2])$/)
export const source = z.enum(['native', 'focus-1.0'])
export const billingDigest = z.string().regex(/^[0-9a-f]{64}$/)
export const billingAmount = z.string().regex(/^-?\d+(\.\d{1,12})?$/)
export const billingRevision = z.string().regex(/^[1-9][0-9]{0,18}$/)

export const billingQuerySchema = z.object({
  account: z.string().trim().min(1).max(128),
  period,
  source,
  revision: billingRevision.optional(),
})

export type BillingQuery = z.infer<typeof billingQuerySchema>
