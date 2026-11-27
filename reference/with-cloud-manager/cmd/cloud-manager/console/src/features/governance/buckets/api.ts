import { z } from 'zod'
import { account, time } from '../../../shared/api/fields'
import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'

function observation<S extends z.ZodType<string>>(state: S) {
  return z.discriminatedUnion('status', [
    z.object({ status: z.literal('unobserved') }),
    z.object({ status: z.enum(['fresh', 'stale']), state, observed_at: time }),
  ])
}

const count = z.int().nonnegative()
const observations = z.object({ fresh: count, stale: count, unobserved: count, flagged: count })
const summary = z.object({ total: count, inventory_unavailable: count, encryption: observations, versioning: observations })
  .refine(({ total, inventory_unavailable, encryption, versioning }) => inventory_unavailable <= total &&
    [encryption, versioning].every((item) => (
      item.fresh + item.stale + item.unobserved === total && item.flagged <= item.fresh && item.unobserved >= inventory_unavailable
    )))

const bucket = z.object({
  id: z.string().min(1), name: z.string().min(1), account, location: z.string().min(1),
  provider: z.enum(['tencent-cvm', 'alibaba-ecs', 'huawei-ecs']), inventory_available: z.boolean(),
  encryption: observation(z.enum(['unconfigured', 'aes256', 'sm4', 'kms'])),
  versioning: observation(z.enum(['disabled', 'enabled', 'suspended'])),
})

const report = pageSchema(bucket).extend({
  as_of: time, window_seconds: z.int().min(60).max(604800), summary,
})

export type BucketProtectionReport = z.infer<typeof report>

export function readBucketProtection(path: string, signal: AbortSignal) {
  return request(path, { signal }, report)
}
