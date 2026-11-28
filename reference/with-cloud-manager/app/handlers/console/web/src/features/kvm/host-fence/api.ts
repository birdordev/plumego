import { request } from '../../session/client'
import { hostDetail, type HostDetail } from '../schema'
import { cleanupReceipt, fenceHistory, workPage, type FenceAction } from './schema'

type HostAction = FenceAction | 'drain' | 'resume'

export function readFence(target: string, signal: AbortSignal) {
  return request(target + '/fence', { signal }, fenceHistory)
}

export function readFenceWork(target: string, signal: AbortSignal, after = '') {
  const query = new URLSearchParams({ limit: '15' })
  if (after) query.set('after', after)
  return request(target + '/fence/work?' + query, { signal }, workPage)
}

export function changeHostState(target: string, host: HostDetail, action: HostAction, signal: AbortSignal,
  evidence?: string, replacement?: string) {
  const controls = { match: host.revision, signal }
  if (action === 'drain' || action === 'resume') {
    return request(target, { ...controls, method: 'PATCH', body: JSON.stringify({ action }) }, hostDetail)
  }
  const body = JSON.stringify({ action, uuid: host.uuid, evidence_sha256: evidence, replacement_agent: replacement })
  return request(target + '/fence', { ...controls, method: 'POST', body }, hostDetail)
}

export function confirmCleanup(target: string, hostRevision: number, vm_id: string, migration_id: string,
  evidence_sha256: string, signal: AbortSignal) {
  const body = JSON.stringify({ vm_id, migration_id, evidence_sha256 })
  return request(target + '/fence/cleanup', { method: 'POST', match: hostRevision, body, signal }, cleanupReceipt)
}

export type { HostAction }
