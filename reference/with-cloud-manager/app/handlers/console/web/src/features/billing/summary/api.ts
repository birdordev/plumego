import { z } from 'zod'
import { account, time } from '../../../shared/api/fields'
import { request } from '../../session/client'
import { billingAmount, billingDigest, billingRevision, period, source } from '../schema'

const provider = z.enum(['tencent-cvm', 'alibaba-ecs', 'huawei-ecs'])
const currency = z.string().regex(/^[A-Z]{3}$/)
const month = period.refine((value) => Number(value.slice(0, 4)) >= 2000)
const monthIndex = (value: string) => Number(value.slice(0, 4)) * 12 + Number(value.slice(5))
const count = z.int().nonnegative()

const summaryScope = z.object({
  from: month, through: month, source, currency, provider: provider.optional(),
}).refine(({ from, through }) => {
  const distance = monthIndex(through) - monthIndex(from)
  return distance >= 0 && distance < 12
})

const query = summaryScope.safeExtend({ missing_only: z.enum(['true', 'false']).optional() })
const monthSummary = z.object({
  period, eligible_accounts: count, published_accounts: count, currency_accounts: count,
  total: z.object({
    currency, billed_cost: billingAmount, effective_cost: billingAmount.optional(), list_cost: billingAmount.optional(),
    expected_billed_cost: billingAmount.optional(), variance: billingAmount.optional(),
  }).optional(),
}).refine((item) => item.currency_accounts <= item.published_accounts && item.published_accounts <= item.eligible_accounts &&
  (item.currency_accounts > 0) === Boolean(item.total))

const accountReport = z.object({
  period, account, provider, billed_cost: billingAmount.optional(),
  report: z.object({ revision: billingRevision, sha256: billingDigest, rows: count }).optional(),
}).refine((item) => item.billed_cost === undefined || item.report !== undefined)

const summarySchema = summaryScope.safeExtend({
  as_of: time, months: z.array(monthSummary).min(1).max(12),
  accounts: z.array(accountReport).max(100), next: z.string().min(1).max(136).optional(),
}).refine((summary) => summary.months.length === monthIndex(summary.through) - monthIndex(summary.from) + 1 &&
  summary.months.every((item, i) => monthIndex(item.period) === monthIndex(summary.from) + i &&
    (!item.total || item.total.currency === summary.currency)) &&
  summary.accounts.every((item) => item.period >= summary.from && item.period <= summary.through &&
    (!summary.provider || item.provider === summary.provider)))

export type ProjectBillingSummary = z.infer<typeof summarySchema>

export function validSummaryFilters(values: URLSearchParams) {
  return query.safeParse(Object.fromEntries(values)).success
}

export async function readBillingSummary(path: string, signal: AbortSignal) {
  const filters = new URLSearchParams(path.split('?')[1])
  const report = await request(path, { signal }, summarySchema)
  if (report.from !== filters.get('from') || report.through !== filters.get('through') ||
    report.source !== filters.get('source') || report.currency !== filters.get('currency') ||
    (report.provider ?? '') !== (filters.get('provider') ?? '') ||
    (filters.get('missing_only') === 'true' && report.accounts.some((item) => item.report))) {
    throw new Error('汇总结果与查询条件不一致。')
  }
  return report
}
