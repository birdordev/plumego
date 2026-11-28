import type { Operation } from '../schema'
import { RowAction, StateBadge } from '../../../shared/components/RowAction'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { actions } from '../labels'
import { timestamp } from '../../../shared/display/time'
import { showOperation } from '../detail/open'

const queueIssueReasons: Readonly<Partial<Record<string, string>>> = {
  missing_task: '缺少队列记录',
  expired_task: '任务超期',
  dead_letter: '任务进入死信',
  finished_task: '任务已结束',
  lease_recovery_overdue: '超过两分钟未接管',
}

export function OperationsTable({ items, showQueueIssue }: { items: readonly Operation[]; showQueueIssue: boolean }) {
  const operationColumns = ['动作', '资源 ID', '状态', '创建时间']
  const columns = showQueueIssue ? ['队列问题', '详情', ...operationColumns] : [...operationColumns, '详情']
  const rows = items.map((item) => {
    const detail = <RowAction onClick={() => showOperation(item.id)}>查看</RowAction>
    const cells = [
      actions[item.action as keyof typeof actions] || item.action, item.asset_id || '待分配',
      <StateBadge status={item.status} />, timestamp(item.created_at),
    ]
    const reason = item.queue_issue_reason && (queueIssueReasons[item.queue_issue_reason] ?? item.queue_issue_reason)
    return { key: item.id, cells: showQueueIssue ? [reason, detail, ...cells] : [...cells, detail] }
  })
  return <ConsoleTable columns={columns} rows={rows} />
}
