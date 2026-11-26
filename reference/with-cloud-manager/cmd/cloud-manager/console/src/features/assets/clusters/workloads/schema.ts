import { z } from 'zod'
import { snapshotMetaSchema } from '../snapshot'

const count = z.number().int().min(0).max(1_000_000)
const workloadSchema = z.object({
  uid: z.string().min(1).max(128),
  namespace: z.string().min(1).max(63),
  kind: z.enum(['Deployment', 'StatefulSet', 'DaemonSet', 'Job', 'CronJob']),
  name: z.string().min(1).max(253),
  desired: count.optional(),
  ready: count.optional(),
  active: count.optional(),
  succeeded: count.optional(),
  failed: count.optional(),
  suspended: z.boolean().optional(),
})

export const workloadPageSchema = snapshotMetaSchema(5000).extend({
  workloads: z.array(workloadSchema).max(100),
}).transform(({ workloads, ...page }) => ({ ...page, items: workloads }))

export type Workload = z.infer<typeof workloadSchema>
