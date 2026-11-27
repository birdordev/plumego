import { request } from '../../session/client'
import type { BillingQuery } from '../schema'
import { billingReportSchema } from './schema'

export async function readBillingReport(base: string, query: BillingQuery, signal: AbortSignal) {
  const params = new URLSearchParams({ account: query.account, period: query.period, source: query.source })
  if (query.revision) params.set('revision', query.revision)
  const report = await request(base + '/billing/reports?' + params, { signal }, billingReportSchema)
  if (report.account !== query.account || report.period !== query.period || report.source !== query.source ||
      (query.revision && String(report.revision) !== query.revision)) {
    throw new Error('账单结果与查询条件不一致。')
  }
  return report
}
