import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { billingQuerySchema } from '../schema'

const currency = z.string().regex(/^[A-Z]{3}$/)
const basis = z.enum(['billed', 'effective'])
export const budgetKeySchema = billingQuerySchema.omit({ revision: true }).extend({ currency, basis })

export const budgetInputSchema = z.object({
  limit: z.string().regex(/^[0-9]{1,26}(\.\d{1,12})?$/).refine((amount) => /[1-9]/.test(amount)),
  warning_percent: z.number().int().min(1).max(99),
  enabled: z.boolean(),
})

const spent = z.string().regex(/^-?\d+(\.\d{1,12})?$/)
export const budgetSchema = budgetKeySchema.extend(budgetInputSchema.shape).extend({
  id: z.uuid(),
  revision,
  updated_at: time,
  state: z.enum(['inactive', 'unavailable', 'normal', 'warning', 'exceeded']),
  spent: spent.optional(),
  report_revision: revision.optional(),
})

export type BudgetKey = z.infer<typeof budgetKeySchema>
export type BudgetInput = z.infer<typeof budgetInputSchema>
export type Budget = z.infer<typeof budgetSchema>
