import { request } from '../../session/client'
import { allocationSchema, type AllocationQuery } from './schema'

export async function readAllocation(base: string, key: AllocationQuery, signal: AbortSignal) {
  const params = new URLSearchParams({ account: key.account, period: key.period, source: key.source })
  if (key.revision) params.set('revision', key.revision)
  if (key.allocation_revision) params.set('allocation_revision', key.allocation_revision)
  const result = await request(base + '/billing/allocations?' + params, { signal }, allocationSchema)
  if (result.account !== key.account || result.period !== key.period || result.source !== key.source ||
      (key.revision && String(result.report_revision) !== key.revision) ||
      (key.allocation_revision && String(result.revision) !== key.allocation_revision)) {
    throw new Error('分摊结果与查询条件不一致。')
  }
  return result
}
