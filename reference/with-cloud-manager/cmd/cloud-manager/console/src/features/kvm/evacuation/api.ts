import { request } from '../../session/client'
import { batchHistory, batchSchema, candidatePage, planHistory, planSchema, type Draft } from './schema'

function pagePath(target: string, collection: string, after: string) {
  const params = new URLSearchParams({ limit: '15' })
  if (after) params.set('after', after)
  return target + '/fence/evacuation-' + collection + '?' + params
}

export function listCandidates(target: string, after: string, signal: AbortSignal) {
  return request(pagePath(target, 'candidates', after), { signal }, candidatePage)
}

export function listBatches(target: string, after: string, signal: AbortSignal) {
  return request(pagePath(target, 'batches', after), { signal }, batchHistory)
}

export function listPlans(target: string, after: string, signal: AbortSignal) {
  return request(pagePath(target, 'plans', after), { signal }, planHistory)
}

export function batchPath(target: string, id: string) {
  return target + '/fence/evacuation-batches/' + encodeURIComponent(id)
}

export function planPath(target: string, id: string) {
  return target + '/fence/evacuation-plans/' + encodeURIComponent(id)
}

export function readBatch(path: string, signal: AbortSignal) {
  return request(path, { signal }, batchSchema)
}

export function readPlan(path: string, signal: AbortSignal) {
  return request(path, { signal }, planSchema)
}

export function cancelPlan(path: string, signal: AbortSignal) {
  return request(path, { method: 'DELETE', signal }, planSchema)
}

export async function submitEvacuation(target: string, { mode, id, host_revision, source_fence_generation, items }: Draft, signal: AbortSignal) {
  const options = { method: 'POST', match: host_revision, signal }
  if (mode === 'plan') {
    const plan = await request(target + '/fence/evacuation-plans', {
      ...options, body: JSON.stringify({ plan_id: id, items }),
    }, planSchema)
    return plan.plan_id
  }
  const batch = await request(target + '/fence/evacuation-batches', {
    ...options, body: JSON.stringify({ batch_id: id, source_fence_generation, items }),
  }, batchSchema)
  return batch.batch_id
}
