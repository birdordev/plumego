import { request } from '../../../session/client'
import type { Asset } from '../../types'
import { currentReclaimSchema, reclaimSchema, transferredOwnershipSchema, type Decision, type Evidence, type Reclaim } from './schema'

function reclaimsURL(project: string) {
  return project + '/bare-metal/reclaims'
}

export function currentReclaimURL(project: string, asset: Asset) {
  return reclaimsURL(project) + '/current?' + new URLSearchParams({
    account: asset.account, region: asset.region, system_id: asset.id,
  })
}

export function readCurrentReclaim(target: string, signal: AbortSignal) {
  return request(target, { signal }, currentReclaimSchema)
}

export function createReclaim(project: string, asset: Asset, sanitizerID: string, key: string) {
  const body = JSON.stringify({ account: asset.account, region: asset.region, system_id: asset.id, sanitizer_id: sanitizerID })
  return request(reclaimsURL(project), { method: 'POST', key, body, match: asset.ownership_revision }, reclaimSchema)
}

function postReclaim(project: string, reclaimID: string, action: string, payload: object) {
  const target = reclaimsURL(project) + '/' + encodeURIComponent(reclaimID) + '/' + action
  return request(target, { method: 'POST', body: JSON.stringify(payload) }, reclaimSchema)
}

export function reviewReclaim(project: string, reclaimID: string, decision: Decision) {
  return postReclaim(project, reclaimID, 'approval', { decision })
}

export function requestRecovery(project: string, reclaimID: string, systemID: string, recoveryID: string, evidence: Evidence) {
  return postReclaim(project, reclaimID, 'recovery', { recovery_id: recoveryID, system_id: systemID, ...evidence })
}

export function reviewRecovery(project: string, reclaimID: string, recoveryID: string, decision: Decision) {
  return postReclaim(project, reclaimID, 'recovery/approval', { recovery_id: recoveryID, decision })
}

export function transferReclaimedAsset(project: string, asset: Asset, reclaim: Reclaim, targetProject: string) {
  const body = JSON.stringify({
    account: asset.account, region: asset.region, kind: 'bare-metal', asset_id: asset.id, target_project: targetProject,
  })
  return request(project + '/assets/ownership', { method: 'PATCH', body, match: reclaim.ownership_revision }, transferredOwnershipSchema)
}
