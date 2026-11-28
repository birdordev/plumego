import { z } from 'zod'
import { request } from '../../session/client'

const limits = z.object({
  max_active: z.int().min(1).max(10000),
  max_managed_instances: z.int().min(0).max(100000),
})

const operationBudget = limits.extend({
  revision: z.string().regex(/^[1-9][0-9]{0,18}$/),
  active_operations: z.int().nonnegative(),
  reserved_instances: z.int().nonnegative(),
})

export type OperationBudget = z.infer<typeof operationBudget>

export function readOperationBudget(target: string, signal: AbortSignal) {
  return request(target, { signal }, operationBudget)
}

export function saveOperationBudget(target: string, revision: string, input: z.infer<typeof limits>, signal: AbortSignal) {
  return request(target, { method: 'PUT', match: revision, body: JSON.stringify(input), signal }, operationBudget)
}
