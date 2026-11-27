import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { evidenceSchema, siteID, vmDetail } from '../schema'
import { hostSchema } from '../provisioning/schema'

const placementID = z.string().min(1).max(128)
export const receiptSchema = z.object({
  migration_id: siteID, target_placement_id: placementID, image_id: siteID, evidence_sha256: evidenceSchema,
  source_fence_generation: revision.optional(), require_verified_backup: z.boolean().optional(), automatic_placement: z.boolean().optional(),
  vm_id: z.guid(), source_host: siteID, source_placement_id: placementID, target_host: siteID, target_generation: revision,
  vm_revision: revision, status: z.enum(['pending', 'approved', 'rejected']), requester: z.string().min(1),
  reviewer: z.string().optional(), requested_at: time, reviewed_at: time.optional(),
  cleanup_at: time.optional(), cleanup_sha256: evidenceSchema.optional(), cleanup_reviewer: z.string().optional(),
})
const available = z.object({ vm: vmDetail })
export const eligibilitySchema = z.discriminatedUnion('mode', [
  available.extend({ mode: z.literal('unavailable') }),
  available.extend({ mode: z.literal('cold') }),
  available.extend({ mode: z.literal('evacuation'), source_fence_generation: revision }),
])
export const attemptSchema = z.object({
  id: z.guid(), source: available.extend({ mode: z.enum(['cold', 'evacuation']), source_fence_generation: revision.optional() }),
  host: hostSchema.optional(), placement: placementID.optional(), image: siteID, evidence: evidenceSchema, verified: z.boolean(), started: z.boolean(),
}).refine(({ source, host }) => source.mode === 'cold' ? host !== undefined : source.source_fence_generation !== undefined)

export type Eligibility = z.infer<typeof eligibilitySchema>
export type Attempt = z.infer<typeof attemptSchema>
export type Receipt = z.infer<typeof receiptSchema>

export const statuses = { pending: '迁移待审批', approved: '已批准并交接目标任务', rejected: '迁移已拒绝' }
