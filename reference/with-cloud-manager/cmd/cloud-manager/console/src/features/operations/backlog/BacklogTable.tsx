import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { providerName } from '../../../shared/display/providers'
import { timestamp } from '../../../shared/display/time'
import type { BacklogCounts, BacklogReport } from './schema'
import './styles.css'

const compactTime = new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })

function backlogCell(count: number, time?: string) {
  if (time === undefined) return count
  const full = timestamp(time)
  return <>{count}<time className="minor" dateTime={time} title={full} aria-label={full}>{compactTime.format(new Date(time))}</time></>
}

type StateTime = 'earliest_ready_at' | 'earliest_recovery_at' | 'next_deferred_at'

const states: readonly { label: string; count: Exclude<keyof BacklogCounts, StateTime>; time?: StateTime }[] = [
  { label: '可领取（最早就绪）', count: 'ready', time: 'earliest_ready_at' },
  { label: '延迟执行（下次到期）', count: 'deferred', time: 'next_deferred_at' },
  { label: '已有租约', count: 'leased' },
  { label: '可接管（最早到期）', count: 'recoverable', time: 'earliest_recovery_at' },
  { label: '待审批', count: 'awaiting_approval' },
  { label: '待复核', count: 'needs_review' },
  { label: '队列异常', count: 'queue_anomalies' },
]

export function BacklogTable({ report }: { report: BacklogReport }) {
  if (report.providers.length === 0) return <p>当前范围没有非终态操作。</p>
  const columns = ['状态', '合计', ...report.providers.map((item) => providerName(item.provider))]
  const groups = [report.summary, ...report.providers]
  const rows = states.map(({ label, count, time }) => ({
    key: count,
    cells: [label, ...groups.map((group) => backlogCell(group[count], time && group[time]))],
  }))
  return <div className="operation-backlog-table"><ConsoleTable columns={columns} rows={rows} /></div>
}
