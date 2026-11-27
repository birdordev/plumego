import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { pageSchema } from '../../../shared/api/page'
import { receiptSchema } from '../cold-migration/schema'
import { evidenceSchema, siteID } from '../schema'

export const maxMembers = 16
export const mode = z.enum(['plan', 'batch'])
export type Mode = z.infer<typeof mode>

const candidate = z.object({
  vm_id: z.guid(), vm_revision: revision, state: z.string().min(1), network: siteID,
  disk_mib: z.int().positive(), ipv4: z.ipv4().optional(),
})
export const candidatePage = pageSchema(candidate).extend({
  mode: mode.or(z.literal('unavailable')), host_revision: revision, source_fence_generation: revision,
})
export type Candidate = z.infer<typeof candidate>

export const member = z.object({
  vm_id: z.guid(), vm_revision: revision, image_id: siteID,
  evidence_sha256: evidenceSchema, require_verified_backup: z.boolean().optional(),
})
export type Member = z.infer<typeof member>

const items = z.array(member).min(1).max(maxMembers)
  .refine((members) => new Set(members.map(({ vm_id }) => vm_id)).size === members.length)

export const draftSchema = z.object({
  id: z.guid(), mode, host_revision: revision, source_fence_generation: revision, items,
  phase: z.enum(['ready', 'sent', 'rejected']),
})
export type Draft = z.infer<typeof draftSchema>
export type Source = Pick<Draft, 'mode' | 'host_revision' | 'source_fence_generation'>

const planStatus = z.enum(['armed', 'submitted', 'cancelled', 'failed'])
export const planLabels: Record<z.infer<typeof planStatus>, string> = {
  armed: '已登记', submitted: '已生成批次', cancelled: '已撤销', failed: '已终止',
}

const record = z.object({ id: siteID, source_fence_generation: revision, requester: z.string().min(1), recorded_at: time })
export const batchHistory = pageSchema(record)
export const planHistory = pageSchema(record.extend({ status: planStatus, batch_id: siteID.optional() }))

const source = z.object({
  source_host: siteID, source_fence_generation: revision, host_revision: revision, requester: z.string().min(1),
})
export const planSchema = source.extend({
  plan_id: siteID, items, status: planStatus, last_result: z.string().optional(),
  armed_at: time, activated_at: time.optional(), next_attempt_at: time.optional(), finished_at: time.optional(),
  batch_id: siteID.optional(), cancelled_by: z.string().optional(),
})
export const batchSchema = source.extend({ batch_id: siteID, requested_at: time, items: z.array(receiptSchema).min(1).max(maxMembers) })
