import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'
import { bootImage } from '../schema'
import { cleanupRecord, retainedVolume } from './schema'

const volumePage = pageSchema(retainedVolume)
const cleanupPage = pageSchema(cleanupRecord)

function pageQuery(after: string) {
  const params = new URLSearchParams({ limit: '15' })
  if (after) params.set('after', after)
  return '?' + params
}

export function volumeCleanupsPath(sitePath: string, placement: string) {
  return sitePath + '/placements/' + encodeURIComponent(placement) + '/volume-cleanups'
}

export function listVolumes(vm: string, after: string, signal: AbortSignal) {
  return request(vm + '/retained-volumes' + pageQuery(after), { signal }, volumePage)
}

export function listCleanups(collection: string, after: string, signal: AbortSignal) {
  return request(collection + pageQuery(after), { signal }, cleanupPage)
}

export function readBackup(site: string, id: string, signal: AbortSignal) {
  return request(site + '/images/' + encodeURIComponent(id), { signal }, bootImage)
}

export function readCleanup(target: string, signal: AbortSignal) {
  return request(target, { signal }, cleanupRecord)
}

function postCleanup(target: string, match: number, body: object, signal: AbortSignal) {
  return request(target, { method: 'POST', match, body: JSON.stringify(body), signal }, cleanupRecord)
}

export function requestCleanup(target: string, hostRevision: number, id: string, backup: string, evidence: string, signal: AbortSignal) {
  return postCleanup(target, hostRevision, { cleanup_id: id, backup_image_id: backup, evidence_sha256: evidence }, signal)
}

export function reviewCleanup(target: string, revision: number, decision: 'approve' | 'reject', signal: AbortSignal) {
  return postCleanup(target + '/review', revision, { decision }, signal)
}

export function resolveCleanup(target: string, revision: number, proof: {
  observed: 'present' | 'absent'; evidence_sha256: string; second_evidence_sha256?: string
}, signal: AbortSignal) {
  return postCleanup(target + '/resolve', revision, proof, signal)
}
