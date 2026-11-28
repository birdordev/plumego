import { z } from 'zod'
import { revision, time } from '../../shared/api/fields'

export const siteIDPattern = String.raw`[A-Za-z0-9][A-Za-z0-9_.\-]{0,63}`
export const siteID = z.string().regex(new RegExp('^' + siteIDPattern + '$'))
export const evidenceSchema = z.string().regex(/^[a-f0-9]{64}$/)
export const bootImage = z.object({ image_id: siteID, image_sha256: evidenceSchema, image_size_bytes: z.int().positive() })
const vmID = z.guid()
export const capacity = z.object({
  vcpu: z.int().nonnegative(), memory_mib: z.int().nonnegative(), disk_mib: z.int().nonnegative(), gpu: z.int().nonnegative(),
})

export const hostSummary = z.object({
  site: siteID, id: siteID, failure_domain: siteID, draining: z.boolean(),
  fence_state: z.string().optional(), reported_at: time.optional(),
})

export const hostDetail = hostSummary.extend({
  uuid: z.guid(), revision, generation: revision, agent: siteID, networks: z.array(siteID).max(32),
  storage_pool: siteID, shared_storage_pool_id: siteID.optional(), capacity,
})

const vmFields = z.object({ id: vmID, host: siteID, state: z.string().min(1), ipv4: z.ipv4().optional() })

export const vmSummary = vmFields.extend({ site: siteID })
export const vmDetail = vmFields.extend({
  revision, generation: revision, placement_id: z.string().min(1), capacity,
  network: siteID, storage_pool: siteID, shared_storage_pool_id: siteID.optional(),
  boot_volume: z.string().min(1), image_id: siteID.optional(), mac: z.string().optional(), observation: z.string().optional(),
})

export type HostSummary = z.infer<typeof hostSummary>
export type HostDetail = z.infer<typeof hostDetail>
export type VMDetail = z.infer<typeof vmDetail>
export type InventoryItem = ({ kind: 'hosts' } & HostSummary) | ({ kind: 'vms' } & z.infer<typeof vmSummary>)
export type InventoryKind = InventoryItem['kind']
