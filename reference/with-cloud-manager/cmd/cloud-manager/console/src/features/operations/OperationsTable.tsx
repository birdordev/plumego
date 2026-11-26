import type { z } from 'zod'
import type { operationSchema } from './schema'
import { RowAction, StateBadge } from '../../shared/components/RowAction'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { actions } from './labels'
import { timestamp } from '../../shared/display/time'
import { showOperation } from './detail/open'

type Operation = z.infer<typeof operationSchema>

export function OperationsTable({ items }: { items: readonly Operation[] }) {
  const columns = ['动作', '资源 ID', '状态', '创建时间', '详情']
  const rows = items.map((item) => ({
    key: item.id,
    cells: [
      actions[item.action as keyof typeof actions] || item.action, item.asset_id || '待分配',
      <StateBadge status={item.status} />, timestamp(item.created_at),
      <RowAction onClick={() => showOperation(item.id)}>查看</RowAction>,
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
