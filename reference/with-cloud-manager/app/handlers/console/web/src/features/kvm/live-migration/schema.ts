import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { siteID } from '../schema'

export const migrationSchema = z.object({
  migration_id: siteID, vm_id: z.guid(), source_host: siteID, target_host: siteID,
  revision, vm_revision: revision, source_generation: revision, target_generation: revision,
  status: z.enum(['pending', 'approved', 'dispatched', 'source_departed', 'completed', 'rejected', 'aborted']),
  requester: z.string().min(1), reviewer: z.string().min(1).optional(),
  requested_at: time, reviewed_at: time.optional(), prepared_at: time.optional(), dispatched_at: time.optional(),
  source_departed_at: time.optional(), completed_at: time.optional(), aborted_at: time.optional(),
  aborted_by: z.string().min(1).optional(), abort_evidence_sha256: z.string().optional(),
})

export const targetSchema = z.object({ id: siteID, failure_domain: siteID })
export type Migration = z.infer<typeof migrationSchema>
export type Target = z.infer<typeof targetSchema>
