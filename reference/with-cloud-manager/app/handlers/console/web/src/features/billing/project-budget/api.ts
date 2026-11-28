import { z } from 'zod'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { account, time } from '../../../shared/api/fields'
import { request } from '../../session/client'
import { budgetInputSchema, budgetKeySchema } from '../budgets/schema'
import { billingAmount, billingRevision } from '../schema'

export const projectBudgetKey = budgetKeySchema.omit({ account: true })
const state = z.enum(['inactive', 'unavailable', 'normal', 'warning', 'exceeded'])
const accountStatus = z.object({
  account, state: z.enum(['account_unavailable', 'report_missing', 'amount_missing', 'observed']),
  report_revision: billingRevision.optional(), amount: billingAmount.optional(),
}).refine((item) => (item.state === 'observed') === (item.amount !== undefined))

export const projectBudgetInput = budgetInputSchema.safeExtend({
  accounts: z.array(account).min(1).max(100).refine((items) => new Set(items).size === items.length),
})

const projectBudgetSchema = projectBudgetKey.extend({
  id: z.uuid(), limit: budgetInputSchema.shape.limit, warning_percent: budgetInputSchema.shape.warning_percent, enabled: z.boolean(),
  revision: billingRevision, updated_at: time, state, spent: billingAmount.optional(), observed_accounts: z.int().nonnegative(),
  accounts: z.array(accountStatus).min(1).max(100),
}).refine((budget) => budget.observed_accounts === budget.accounts.filter((item) => item.state === 'observed').length &&
  (budget.spent !== undefined) === (budget.observed_accounts === budget.accounts.length))

export type ProjectBudget = z.infer<typeof projectBudgetSchema>
export type ProjectBudgetInput = z.infer<typeof projectBudgetInput>

function validateResult(path: string, budget: ProjectBudget) {
  const params = new URLSearchParams(path.split('?')[1])
  if (budget.period !== params.get('period') || budget.source !== params.get('source') ||
    budget.currency !== params.get('currency') || budget.basis !== params.get('basis')) {
    throw new Error('预算结果与查询条件不一致。')
  }
  return budget
}

export async function readProjectBudget(path: string, signal: AbortSignal) {
  try {
    return validateResult(path, await request(path, { signal }, projectBudgetSchema))
  } catch (error) {
    if (error instanceof ConsoleRequestError && error.status === 404) return undefined
    throw error
  }
}

export async function saveProjectBudget(path: string, input: ProjectBudgetInput, revision: string | undefined, signal: AbortSignal) {
  return validateResult(path, await request(path, {
    method: revision ? 'PUT' : 'POST', body: JSON.stringify(input), match: revision, signal,
  }, projectBudgetSchema))
}
