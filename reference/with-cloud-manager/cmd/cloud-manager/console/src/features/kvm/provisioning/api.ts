import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'
import { bootImage, vmDetail } from '../schema'
import { hostSchema, placementSchema, provisioningStatus, type Draft } from './schema'

const hostsPage = pageSchema(hostSchema)
const imagesPage = pageSchema(bootImage)

function pageQuery(after: string, site?: string) {
  const query = new URLSearchParams({ limit: '15' })
  if (site) query.set('site', site)
  if (after) query.set('after', after)
  return '?' + query
}

export function listHosts(base: string, after: string, signal: AbortSignal, site?: string) {
  return request(base + '/kvm/provisioning-hosts' + pageQuery(after, site), { signal }, hostsPage)
}

export function listImages(sitePath: string, after: string, signal: AbortSignal) {
  return request(sitePath + '/images' + pageQuery(after), { signal }, imagesPage)
}

export function readProvisioning(target: string, signal: AbortSignal) {
  return request(target + '/provisioning', { signal }, provisioningStatus)
}

export function readPlacement(target: string, signal: AbortSignal) {
  return request(target, { signal }, placementSchema)
}

export function reservePlacement(sitePath: string, key: string, input: object, signal: AbortSignal) {
  return request(sitePath + '/placements', {
    method: 'POST', key, body: JSON.stringify(input), signal,
  }, placementSchema)
}

export function submitVM(sitePath: string, placement: string, boot: Draft['boot'], signal: AbortSignal) {
  const body = JSON.stringify({ placement_id: placement, ...(boot.kind === 'image' ? { image_id: boot.id } : { boot_volume: boot.id }) })
  return request(sitePath + '/vms', { method: 'POST', body, signal }, vmDetail)
}

export function releasePlacement(target: string, signal: AbortSignal) {
  return request(target, { method: 'DELETE', signal }, placementSchema)
}
