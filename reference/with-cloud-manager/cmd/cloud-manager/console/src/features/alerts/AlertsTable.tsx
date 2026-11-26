import type { z } from 'zod'
import type { alertSchema } from './schema'
import { RowAction, StateBadge } from '../../shared/components/RowAction'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { alertRules, kinds, timestamp } from '../../shared/display/labels'
import { showAlert } from './detail'

type Alert = z.infer<typeof alertSchema>

export function AlertsTable({ items }: { items: readonly Alert[] }) {
  const columns = ['触发时间', '规则', '账号 / 地域', '对象', '状态', '详情']
  const rows = items.map((item) => ({
    key: item.id,
    cells: [
      timestamp(item.opened_at), alertRules[item.rule as keyof typeof alertRules] || item.rule,
      item.region ? item.account + ' / ' + item.region : item.account,
      item.rule.startsWith('operation_') ? item.subject_id : kinds[item.resource_kind as keyof typeof kinds] || item.resource_kind,
      <StateBadge status={item.status} />, <RowAction onClick={() => showAlert(item.id)}>查看</RowAction>,
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
