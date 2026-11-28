import { z } from 'zod'
import { account, time } from '../../../shared/api/fields'
import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'

const count = z.int().nonnegative()
const state = z.enum(['fresh', 'stale', 'pending'])
const dispatchState = z.enum(['scheduled', 'ready', 'account_busy', 'cooldown', 'leased'])

const scope = z.object({
  account, provider: z.string().min(1), region: z.string(), kind: z.string().min(1), state,
  running: z.boolean(), completed_at: time.optional(), next_at: time,
  dispatch_state: dispatchState.optional(), schedule_lease_until: time.optional(),
  account_lease_until: time.optional(), account_cooldown_until: time.optional(),
})

const summary = z.object({ total: count, fresh: count, stale: count, pending: count })
  .refine(({ total, fresh, stale, pending }) => total === fresh + stale + pending)

const report = pageSchema(scope).extend({
  as_of: time, window_seconds: z.int().min(60).max(604800), summary,
  dispatch_summary: z.object({
    scheduled: count, ready: count, account_busy: count, cooldown: count, leased: count,
    longest_ready_overdue_seconds: count,
  }).optional(),
})

export type FreshnessReport = z.infer<typeof report>
export type DispatchState = z.infer<typeof dispatchState>
export type FreshnessScope = z.infer<typeof scope>

export function readInventoryFreshness(path: string, signal: AbortSignal) {
  return request(path, { signal }, report)
}
