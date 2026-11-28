import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { bootImage, evidenceSchema, siteID } from '../schema'

export const cleanupStatus = z.enum(['pending', 'approved', 'dispatched', 'completed', 'rejected', 'aborted'])
const placementID = z.string().min(1).max(128)
export const retainedVolume = z.object({
  placement_id: placementID, host: siteID, boot_volume: z.string().min(1), storage_pool: siteID,
  shared_storage_pool_id: siteID.optional(), host_revision: revision,
  max_backup_bytes: z.int().positive(), expected_backup_bytes: z.int().positive().optional(),
  retained: z.boolean(), requestable: z.boolean(), latest_status: cleanupStatus.optional(),
})

export const cleanupRecord = z.object({
  cleanup_id: siteID, placement_id: placementID, vm_id: z.guid(), host: siteID, host_generation: revision,
  host_revision: revision, revision, status: cleanupStatus, backup_image_id: siteID, evidence_sha256: evidenceSchema,
  requester: z.string().min(1), reviewer: z.string().min(1).optional(), resolved_by: z.string().min(1).optional(),
  requested_at: time, reviewed_at: time.optional(), dispatched_at: time.optional(), first_absent_at: time.optional(), finished_at: time.optional(),
  resolution_sha256: evidenceSchema.optional(), second_resolution_sha256: evidenceSchema.optional(),
})

export type RetainedVolume = z.infer<typeof retainedVolume>
export type BackupImage = z.infer<typeof bootImage>
export type Cleanup = z.infer<typeof cleanupRecord>

export const statuses = {
  pending: '清理待审批', approved: '清理已批准', dispatched: '清理已派发', completed: '清理已完成', rejected: '清理已拒绝', aborted: '卷仍保留，清理已终止',
} satisfies Record<Cleanup['status'], string>

export function isActive(status: Cleanup['status']) {
  return status === 'pending' || status === 'approved' || status === 'dispatched'
}
