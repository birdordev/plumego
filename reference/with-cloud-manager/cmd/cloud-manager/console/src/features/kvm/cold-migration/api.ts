import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'
import { eligibilitySchema, receiptSchema, type Attempt, type Receipt } from './schema'

const historySchema = pageSchema(receiptSchema)

export function readEligibility(target: string, signal: AbortSignal) {
  return request(target + '/cold-migration-eligibility', { signal }, eligibilitySchema)
}

export function listColdMigrations(target: string, after: string, signal: AbortSignal) {
  const params = new URLSearchParams({ limit: '15' })
  if (after) params.set('after', after)
  return request(target + '/cold-migrations?' + params, { signal }, historySchema)
}

export function readColdReceipt(path: string, signal: AbortSignal) {
  return request(path, { signal }, receiptSchema)
}

export function submitColdMigration(target: string, draft: Attempt, signal: AbortSignal) {
  const body = JSON.stringify({ migration_id: draft.id, target_placement_id: draft.placement, image_id: draft.image,
    evidence_sha256: draft.evidence, require_verified_backup: draft.verified, source_fence_generation: draft.source.source_fence_generation })
  return request(target + '/cold-migration', { method: 'POST', match: draft.source.vm.revision, body, signal }, receiptSchema)
}

export function reviewColdMigration(target: string, receipt: Receipt, decision: 'approve' | 'reject', signal: AbortSignal) {
  const body = JSON.stringify({ migration_id: receipt.migration_id, decision })
  return request(target + '/cold-migration/review', { method: 'POST', match: receipt.vm_revision, body, signal }, receiptSchema)
}
