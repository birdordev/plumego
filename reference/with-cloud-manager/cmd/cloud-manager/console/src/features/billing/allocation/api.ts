import { request } from '../../session/client'
import { allocationPreviewSchema, allocationSchema, type AllocationPreview, type AllocationQuery, type AllocationWriteQuery } from './schema'

export async function readAllocation(base: string, key: AllocationQuery, signal: AbortSignal) {
  const params = new URLSearchParams({ account: key.account, period: key.period, source: key.source })
  if (key.revision) params.set('revision', key.revision)
  if (key.allocation_revision) params.set('allocation_revision', key.allocation_revision)
  const result = await request(base + '/billing/allocations?' + params, { signal }, allocationSchema)
  if (result.account !== key.account || result.period !== key.period || result.source !== key.source ||
      (key.revision && (result.report_revision_text ?? String(result.report_revision)) !== key.revision) ||
      (key.allocation_revision && (result.revision_text ?? String(result.revision)) !== key.allocation_revision)) {
    throw new Error('分摊结果与查询条件不一致。')
  }
  return result
}

export async function previewAllocation(base: string, key: AllocationWriteQuery, body: string, signal: AbortSignal) {
  const result = await request(base + '/billing/allocations/preview?' + new URLSearchParams(key),
    { method: 'POST', body, signal }, allocationPreviewSchema)
  if (result.account !== key.account || result.period !== key.period || result.source !== key.source ||
      result.report_revision_text !== key.revision) throw new Error('预览结果与所选账单不一致。')
  return result
}

export async function publishAllocation(base: string, key: AllocationWriteQuery, body: string, preview: AllocationPreview, signal: AbortSignal) {
  const query = new URLSearchParams({ ...key, report_sha256: preview.report_sha256, policy_sha256: preview.policy_sha256 })
  const result = await request(base + '/billing/allocations?' + query,
    { method: 'POST', body, signal, match: preview.current_revision_text }, allocationSchema)
  if (result.account !== key.account || result.period !== key.period || result.source !== key.source ||
      result.report_revision_text !== key.revision || result.report_sha256 !== preview.report_sha256 ||
      result.policy_sha256 !== preview.policy_sha256) throw new Error('发布回执与预览不一致，请查询原账单确认。')
  return result
}
