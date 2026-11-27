import { request } from '../../../session/client'
import type { HostDetail } from '../../schema'
import { recoverySchema, recoveryView, type Observation, type Recovery } from './schema'

export function readRecovery(target: string, signal: AbortSignal) {
  return request(target + '/fence-recovery', { signal }, recoveryView)
}

export function requestRecovery(target: string, match: number, host: HostDetail, id: string,
  observed: Observation, evidence: string, second: string | undefined, signal: AbortSignal) {
  const body = JSON.stringify({ recovery_id: id, host_uuid: host.uuid, fence_generation: host.generation,
    observed, evidence_sha256: evidence, second_evidence_sha256: second })
  return request(target + '/fence-recovery', { method: 'POST', body, match, signal }, recoverySchema)
}

export function reviewRecovery(target: string, receipt: Recovery, decision: 'approve' | 'reject', signal: AbortSignal) {
  return request(target + '/fence-recovery/review', {
    method: 'POST', match: receipt.vm_revision, signal, body: JSON.stringify({ recovery_id: receipt.recovery_id, decision }),
  }, recoverySchema)
}
