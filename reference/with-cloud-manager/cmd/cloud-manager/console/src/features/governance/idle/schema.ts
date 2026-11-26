import { z } from 'zod'
import { pageSchema } from '../../../shared/api/page'

const evidence = z.object({
  from: z.iso.datetime({ offset: true }), until: z.iso.datetime({ offset: true }),
  period_seconds: z.union([z.literal(600), z.literal(3600)]),
  expected_samples: z.int().positive(), cpu_samples: z.int().nonnegative(), network_samples: z.int().nonnegative(),
  cpu_peak_percent: z.number().min(0).max(100).optional(),
  network_peak_bytes_per_second: z.number().nonnegative().optional(),
  evidence_sha256: z.string().regex(/^[a-f0-9]{64}$/),
})

const instance = z.object({
  account: z.string(), region: z.string(), provider: z.string(), id: z.string(), name: z.string(),
  state: z.enum(['candidate', 'active', 'insufficient', 'not_running']),
  reason: z.enum(['inventory_stale', 'instance_not_running', 'metrics_missing', 'binding_changed', 'metrics_stale', 'incomplete_metrics']).optional(),
  evidence: evidence.optional(),
})

export const idlePageSchema = pageSchema(instance).extend({
  cpu_threshold_percent: z.number(), network_threshold_bytes_per_second: z.number(),
})

export type IdleInstance = z.infer<typeof instance>
