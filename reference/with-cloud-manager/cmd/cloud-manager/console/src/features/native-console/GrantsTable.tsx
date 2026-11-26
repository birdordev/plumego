import type { z } from 'zod'
import type { grantSchema } from './schema'
import { RowAction, StateBadge } from '../../shared/components/RowAction'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { grantPurposes, timestamp } from '../../shared/display/labels'
import { showNativeGrant } from './grant'

type Grant = z.infer<typeof grantSchema>

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
