import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { providerName } from '../../../shared/display/providers'
import { timestamp } from '../../../shared/display/time'
import type { BillingReportLink } from '../report-link'
import type { ProjectBillingSummary } from './api'
import './styles.css'

export function BillingSummaryResults({ summary, billLink }: { summary: ProjectBillingSummary; billLink: BillingReportLink }) {
  const source = summary.source
  return <div className="collection-page billing-summary-results">
    <section className="billing-summary-months" aria-labelledby="billing-monthly-title">
      <h2 id="billing-monthly-title">月度汇总 · {summary.currency}</h2>
      <p className="minor">统计时间 {timestamp(summary.as_of)}。费用按已发布账单汇总。</p>
      <ConsoleTable columns={['账期', '已出账账号', '含此币种', '账单金额', '摊销成本', '目录价', '对账控制总额', '差额']}
        rows={summary.months.map((item) => ({ key: item.period, cells: [
          item.period, `${item.published_accounts} / ${item.eligible_accounts}`, item.currency_accounts,
          item.total?.billed_cost, item.total?.effective_cost, item.total?.list_cost, item.total?.expected_billed_cost, item.total?.variance,
        ] }))} />
    </section>
    <section className="billing-summary-accounts" aria-labelledby="billing-account-title">
      <h2 id="billing-account-title">账号账单</h2>
      <ConsoleTable columns={['账期', '云账号', '云厂商', '发布状态', `账单金额（${summary.currency}）`, '来源版本']}
        rows={summary.accounts.map((item) => ({ key: item.period + '/' + item.account, cells: [
          item.period, item.account, providerName(item.provider),
          item.report ? item.report.rows === 0 ? '已发布（零记录）' : '已发布' : '待导入', item.billed_cost,
          item.report ? <a {...billLink({ account: item.account, period: item.period, source, revision: item.report.revision })}>
            查看版本 {item.report.revision}
          </a> : undefined,
        ] }))} />
    </section>
  </div>
}
