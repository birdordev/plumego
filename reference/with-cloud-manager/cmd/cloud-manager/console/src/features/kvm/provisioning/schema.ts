import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { bootImage, capacity, siteID } from '../schema'

export const hostSchema = z.object({
  id: siteID, site: siteID, networks: z.array(siteID).min(1).max(32),
  storage_pool: siteID, shared_storage_pool_id: siteID.optional(),
})
const placementID = z.string().min(1).max(128)
export const placementSchema = z.object({
  id: placementID, site: siteID, host: siteID, network: siteID, storage_pool: siteID, shared_storage_pool_id: siteID.optional(),
  capacity, expires_at: time, ipv4: z.ipv4().optional(), mac: z.string().optional(), state: z.enum(['held', 'bound', 'expired', 'released']),
})
export const provisioningStatus = z.object({ placement: placementSchema, vm_id: z.guid().optional() })

export const capacityInput = z.object({
  vcpu: z.coerce.number().int().min(1).max(4096), memory_mib: z.coerce.number().int().min(1).max(16777216),
  disk_mib: z.coerce.number().int().min(1).max(1099511627776), network: siteID, ipv4: z.union([z.ipv4(), z.literal('')]),
})

const capacityRequest = capacityInput.extend({
  required_host: siteID, storage_pool: siteID, shared_storage_pool_id: siteID.optional(), ttl_seconds: z.literal(300),
})
export const bootVolume = siteID.refine((name) => !name.startsWith('cmp-image-'))
const bootSource = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('image'), id: siteID }), z.object({ kind: z.literal('volume'), id: bootVolume }),
])

export const draftSchema = z.object({
  key: z.guid(), site: siteID, request: capacityRequest, boot: bootSource, attempted: z.boolean(), placement: placementID.optional(),
}).refine(({ request, boot }) => !request.shared_storage_pool_id || boot.kind === 'image')

export type HostOption = z.infer<typeof hostSchema>
export type Image = z.infer<typeof bootImage>
export type Placement = z.infer<typeof placementSchema>
export type Draft = z.infer<typeof draftSchema>
