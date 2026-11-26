import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { DetailFields } from '../../shared/components/DetailFields'
import { timestamp } from '../../shared/display/time'
import type { BillingReport } from './schema'

export function BillingReportView({ report }: { report: BillingReport }) {
  const billed = report.source === 'native' ? '厂商账单金额' : '开票口径金额（FOCUS）'
  return <>
    <h2>{report.account} · {report.period}</h2>
    <DetailFields fields={[
      ['版本', report.revision + (report.current ? '（当前）' : '（历史）')],
      ['发布于', timestamp(report.imported_at)],
      ['账单记录', report.rows],
      ['报表 SHA-256', <code>{report.sha256}</code>],
    ]} />
    <ConsoleTable columns={['原币种', billed, '摊销成本', '目录价', '对账控制总额', '差额']} rows={report.totals.map((total) => ({
      key: total.currency,
      cells: [total.currency, total.billed_cost, total.effective_cost, total.list_cost, total.expected_billed_cost, total.variance],
    }))} />
    <details>
      <summary>归档原件（{report.parts.length}）</summary>
      <ConsoleTable columns={['编号', 'SHA-256', '字节数', '记录数']} rows={report.parts.map((part) => ({
        key: String(part.number),
        cells: [part.number, <code>{part.sha256}</code>, part.bytes, part.rows],
      }))} />
    </details>
  </>
}
