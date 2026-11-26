import { request } from '../../session/client'
import { budgetSchema, type Budget, type BudgetInput, type BudgetKey } from './schema'

function budgetURL(base: string, key: BudgetKey) {
  return base + '/billing/budget?' + new URLSearchParams(key)
}

function checkScope(budget: Budget, key: BudgetKey) {
  if (budget.account !== key.account || budget.period !== key.period || budget.source !== key.source ||
      budget.currency !== key.currency || budget.basis !== key.basis) throw new Error('预算与查询条件不一致。')
  return budget
}

export async function readBudget(base: string, key: BudgetKey, signal: AbortSignal) {
  return checkScope(await request(budgetURL(base, key), { signal }, budgetSchema), key)
}

export async function writeBudget(base: string, key: BudgetKey, input: BudgetInput, revision?: number) {
  return checkScope(await request(budgetURL(base, key), {
    method: revision === undefined ? 'POST' : 'PUT', body: JSON.stringify(input), match: revision,
  }, budgetSchema), key)
}
