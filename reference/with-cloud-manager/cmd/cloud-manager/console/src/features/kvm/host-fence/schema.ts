import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { evidenceSchema, hostDetail, siteID as identifier } from '../schema'

export const fenceAction = z.enum(['request', 'confirm', 'request-rejoin', 'approve-rejoin', 'reject-rejoin'])
const event = z.object({
  revision, generation: revision, actor: z.string().min(1), action: fenceAction, occurred_at: time,
  evidence_sha256: z.union([evidenceSchema, z.literal('')]).optional(), replacement_agent: identifier.optional(),
})

export const fenceHistory = z.object({ host: hostDetail, events: z.array(event).max(20) })
const workItem = z.object({
  vm_id: z.guid(), placement_id: z.string().min(1), vm_revision: revision, vm_generation: revision,
  state: z.string().min(1), observation: z.string(), pending_recovery_id: identifier.optional(),
  evacuation_id: identifier.optional(), live_migration_id: identifier.optional(),
})

export const workPage = z.object({
  host: identifier, host_uuid: z.guid(), host_revision: revision, host_generation: revision,
  fence_state: z.enum(['', 'pending', 'fenced', 'rejoin_pending']),
  dispatched_volume_cleanups: z.int().nonnegative().default(0), items: z.array(workItem).max(100), next: z.string().min(1).optional(),
})

export const cleanupReceipt = z.object({ vm_id: z.guid(), migration_id: identifier, cleanup_at: time })
export type FenceAction = z.infer<typeof fenceAction>
export type FenceHistory = z.infer<typeof fenceHistory>
export type WorkPage = z.infer<typeof workPage>
export type WorkItem = z.infer<typeof workItem>
