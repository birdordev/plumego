import type { Grant } from '../schema'
import { RowAction, StateBadge } from '../../../shared/components/RowAction'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { grantPurposes } from '../labels'
import { timestamp } from '../../../shared/display/time'
import { showNativeGrant } from '../access/detail'

export function GrantsTable({ items }: { items: readonly Grant[] }) {
  const columns = ['实例', '用途', '申请人', '状态', '有效期', '详情']
  const rows = items.map((item) => ({
    key: item.id,
    cells: [
      item.asset_id, grantPurposes[item.purpose as keyof typeof grantPurposes] || item.purpose, item.requester, <StateBadge status={item.status} />,
      timestamp(item.expires_at), <RowAction onClick={() => showNativeGrant(item.id)}>查看</RowAction>,
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
