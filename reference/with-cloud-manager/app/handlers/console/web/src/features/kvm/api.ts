import { pageSchema } from '../../shared/api/page'
import { collectionReader, type PageRequest } from '../../shared/collection/read-page'
import { request } from '../session/client'
import { hostDetail, hostSummary, vmDetail, vmSummary, type InventoryItem, type InventoryKind } from './schema'

const removeKind = (query: URLSearchParams) => query.delete('kind')
const hosts = collectionReader(request, '/kvm/hosts', pageSchema(hostSummary), 'after', removeKind)
const vms = collectionReader(request, '/kvm/vms', pageSchema(vmSummary), 'after', removeKind)

export function inventoryKind(query: URLSearchParams): InventoryKind {
  return query.get('kind') === 'hosts' ? 'hosts' : 'vms'
}

export async function readInventory(query: PageRequest) {
  const kind = inventoryKind(new URLSearchParams(query.filters))
  if (kind === 'hosts') {
    const page = await hosts(query)
    return { ...page, items: page.items.map((item): InventoryItem => ({ ...item, kind })) }
  }
  const page = await vms(query)
  return { ...page, items: page.items.map((item): InventoryItem => ({ ...item, kind })) }
}

export function detailPath(base: string, { site, kind, id }: InventoryItem) {
  return kvmSitePath(base, site) + '/' + kind + '/' + encodeURIComponent(id)
}

export function kvmSitePath(base: string, site: string) {
  return base + '/kvm/sites/' + encodeURIComponent(site)
}

export function readHost(target: string, signal: AbortSignal) {
  return request(target, { signal }, hostDetail)
}

export function readVM(target: string, signal: AbortSignal) {
  return request(target, { signal }, vmDetail)
}

export type VMAction = 'shutdown' | 'retirement' | 'approve' | 'reject'

export function changeVM(target: string, action: VMAction, match: number, signal: AbortSignal) {
  switch (action) {
    case 'shutdown':
    case 'retirement':
      return request(target + '/' + action, { method: 'POST', match, signal }, vmDetail)
    case 'approve':
    case 'reject':
      return request(target + '/review', { method: 'POST', match, signal, body: JSON.stringify({ decision: action }) }, vmDetail)
  }
}
