import { z } from 'zod'
import { account, time } from '../../../shared/api/fields'
import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'

const status = z.enum(['ready', 'outdated', 'missing', 'stale', 'unobserved', 'unsupported'])
const evidence = z.object({
  status, observed_at: time.optional(), latest_evidence_at: time.optional(),
  scope: z.enum(['instance', 'source_instance']).optional(), schedule_enabled: z.boolean().optional(),
  last_refresh_outcome: z.enum(['success', 'failure', 'throttled']).optional(), last_refresh_at: time.optional(),
})
const count = z.int().nonnegative()
const counts = z.object({ ready: count, outdated: count, missing: count, stale: count, unobserved: count, unsupported: count })
const summary = z.object({
  total: count, inventory_unavailable: count, refresh_failed: count, backups: counts, restore_windows: counts,
}).refine(({ total, inventory_unavailable, refresh_failed, backups, restore_windows }) => (
  inventory_unavailable <= total && refresh_failed <= total &&
  [backups, restore_windows].every((row) => Object.values(row).reduce((sum, value) => sum + value, 0) === total)
))
const item = z.object({
  id: z.string(), name: z.string(), account, region: z.string(), provider: z.string(),
  engine: z.string(), product: z.string(), inventory_available: z.boolean(),
  backups: evidence, restore_windows: evidence,
})
const report = pageSchema(item).extend({
  as_of: time, window_seconds: z.int().min(60).max(604800),
  max_age_seconds: z.int().min(60).max(2592000), summary,
})

export type DatabaseProtectionReport = z.infer<typeof report>
export type DatabaseEvidence = z.infer<typeof evidence>

export function readDatabaseProtection(path: string, signal: AbortSignal) {
  return request(path, { signal }, report)
}
