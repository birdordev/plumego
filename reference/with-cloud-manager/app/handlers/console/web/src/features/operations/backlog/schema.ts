import { z } from 'zod'

const count = z.number().int().nonnegative()
const instant = z.iso.datetime({ offset: true })

const counts = z.object({
  awaiting_approval: count,
  ready: count,
  deferred: count,
  leased: count,
  recoverable: count,
  needs_review: count,
  queue_anomalies: count,
  earliest_ready_at: instant.optional(),
  earliest_recovery_at: instant.optional(),
  next_deferred_at: instant.optional(),
})

export const backlogSchema = z.object({
  as_of: instant,
  summary: counts,
  providers: z.array(counts.extend({ provider: z.string().min(1) })),
})

export type BacklogReport = z.infer<typeof backlogSchema>
export type BacklogCounts = z.infer<typeof counts>
