import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { MetricStrip } from '../../../shared/components/MetricStrip'
import { StateBadge } from '../../../shared/components/RowAction'
import { providerName } from '../../../shared/display/providers'
import { timestamp } from '../../../shared/display/time'
import type { FreshnessReport } from './api'
import { DispatchOverview, DispatchStateCell } from './DispatchStatus'
import { freshnessStateLabels, freshnessStates } from './labels'
const percent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 })

export function FreshnessResults({ report }: { report: FreshnessReport }) {
  const { summary, items } = report
  return <>
    <section className="panel report-panel" aria-labelledby="freshness-summary-title">
      <header className="report-heading">
        <h2 id="freshness-summary-title">盘点新鲜度汇总</h2>
        <p className="hint">观测于 {timestamp(report.as_of)} · 窗口 {report.window_seconds} 秒</p>
      </header>
      <MetricStrip values={[
        ['启用范围', summary.total],
        ...freshnessStates.map(([state, label]) => [label, summary[state]] as const),
        ['近期完成比例', summary.total ? percent.format(summary.fresh / summary.total) : '无可统计范围'],
      ]} />
    </section>
    {report.dispatch_summary && <DispatchOverview summary={report.dispatch_summary} />}
    {items.length ? <ConsoleTable
      columns={['账号', '云厂商', '地域', '资源类型', '新鲜度', '调度状态', '最近完整盘点', '下次计划']}
      rows={items.map((item) => ({ key: JSON.stringify([item.account, item.region, item.kind]), cells: [
        item.account, providerName(item.provider), item.region || '全局', item.kind,
        <><StateBadge status={item.state} label={freshnessStateLabels.get(item.state)} />
          {item.running && <span className="minor">正在盘点</span>}</>,
        <DispatchStateCell item={item} />,
        item.completed_at ? timestamp(item.completed_at) : undefined, timestamp(item.next_at),
      ] }))} /> : <p>当前筛选没有盘点范围。</p>}
  </>
}
