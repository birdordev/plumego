import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { inventoryStates, kinds } from '../labels'
import { AssetActions } from './AssetActions'
import { AssetSummary } from './AssetSummary'
import { assetActions } from './asset-actions'
import { InstanceSelection } from '../instances/InstanceSelection'
import type { AssetActionContext } from '../row-action'
import { assetKey, type Asset } from '../types'

export function AssetsTable({ items, editable, ...access }: { items: readonly Asset[]; editable: boolean } & Omit<AssetActionContext, 'canExecute'>) {
  const actionsByRow = items.map((item) => assetActions(item, { ...access, canExecute: editable }))
  const withActions = actionsByRow.some((actions) => actions.length > 0)
  const columns = [...(editable ? ['选择'] : []), '资源', '类型', '账号 / 地域', '状态', '归属版本',
    ...(withActions ? ['操作'] : [])]
  const rows = items.map((item, index) => ({
    key: assetKey(item),
    cells: [
      ...(editable ? [item.kind === 'instance' && item.provider !== 'vmware-vsphere' ? <InstanceSelection asset={item} /> : '—'] : []),
      <AssetSummary asset={item} />,
      kinds[item.kind as keyof typeof kinds] || item.kind,
      item.region ? item.account + ' / ' + item.region : item.account,
      inventoryStates[item.inventory_state as keyof typeof inventoryStates] || item.status || item.inventory_state,
      item.ownership_revision,
      ...(withActions ? [<AssetActions actions={actionsByRow[index]} />] : []),
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
