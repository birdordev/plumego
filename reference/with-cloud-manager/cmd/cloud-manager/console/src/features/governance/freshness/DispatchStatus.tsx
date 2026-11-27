import { MetricStrip } from '../../../shared/components/MetricStrip'
import { timestamp } from '../../../shared/display/time'
import type { DispatchState, FreshnessReport, FreshnessScope } from './api'

const labels = {
  scheduled: '尚未到期', ready: '到期等待领取', account_busy: '同账号执行中',
  cooldown: '账号限流退避', leased: '已领取',
} satisfies Record<DispatchState, string>

export const dispatchOptions = Object.entries(labels)

export function DispatchOverview({ summary }: { summary: NonNullable<FreshnessReport['dispatch_summary']> }) {
  return <section className="panel report-panel" aria-labelledby="dispatch-summary-title">
    <h2 id="dispatch-summary-title">盘点调度汇总</h2>
    <MetricStrip values={[
      [labels.scheduled, summary.scheduled], [labels.ready, summary.ready], [labels.account_busy, summary.account_busy],
      [labels.cooldown, summary.cooldown], [labels.leased, summary.leased],
      ['可领取计划最久逾期（秒）', summary.ready ? summary.longest_ready_overdue_seconds : '无等待计划'],
    ]} />
  </section>
}

export function DispatchStateCell({ item }: { item: FreshnessScope }) {
  if (!item.dispatch_state) return '—'
  const deadline = item.dispatch_state === 'cooldown' ? item.account_cooldown_until
    : item.dispatch_state === 'account_busy' ? item.account_lease_until
    : item.dispatch_state === 'leased' ? item.schedule_lease_until : undefined
  return <>
    {labels[item.dispatch_state]}
    {deadline && <span className="minor">至 {timestamp(deadline)}</span>}
  </>
}
