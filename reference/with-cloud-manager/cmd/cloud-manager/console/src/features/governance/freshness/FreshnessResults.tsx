import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import type { FreshnessReport } from './api'
import { DispatchOverview, DispatchStateCell } from './DispatchStatus'
import { providers, states } from './FreshnessFilters'

const providerLabels = new Map<string, string>(providers)
const stateLabels = new Map<string, string>(states)
const percent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 })

export function FreshnessResults({ report }: { report: FreshnessReport }) {
  const { summary, items } = report
  return <>
    <section aria-label="盘点新鲜度汇总">
      <DetailFields fields={[
        ['观测时间', timestamp(report.as_of)], ['窗口（秒）', report.window_seconds], ['启用范围', summary.total],
        ['近期完成', summary.fresh], ['已过期', summary.stale], ['尚无完整结果', summary.pending],
        ['近期完成比例', summary.total ? percent.format(summary.fresh / summary.total) : '无可统计范围'],
      ]} />
    </section>
    {report.dispatch_summary && <DispatchOverview summary={report.dispatch_summary} />}
    {items.length ? <ConsoleTable
      columns={['账号', '云厂商', '地域', '资源类型', '新鲜度', '调度状态', '最近完整盘点', '下次计划']}
      rows={items.map((item) => ({ key: JSON.stringify([item.account, item.region, item.kind]), cells: [
        item.account, providerLabels.get(item.provider) ?? item.provider, item.region || '全局', item.kind,
        stateLabels.get(item.state) + (item.running ? ' · 正在盘点' : ''),
        <DispatchStateCell item={item} />,
        item.completed_at ? timestamp(item.completed_at) : undefined, timestamp(item.next_at),
      ] }))} /> : <p>当前筛选没有盘点范围。</p>}
  </>
}
