import type { ListAssetActions } from '../row-action'
import { showBackups } from './backups'
import { showDatabaseRename } from './rename'


const productsByProvider = new Map<string, readonly string[]>([
  ['tencent-cvm', ['tencent-cdb', 'tencent-postgresql', 'tencent-sqlserver']],
  ['alibaba-ecs', ['alibaba-rds']],
  ['huawei-ecs', ['huawei-rds']],
])

export const databaseActions: ListAssetActions = (asset, { canExecute }) => {
  const product = asset.database_instance?.product
  if (asset.inventory_state !== 'observed' || !product || !productsByProvider.get(asset.provider)?.includes(product)) return []
  const actions = [{ label: '查看备份', run: () => showBackups(asset) }]
  if (canExecute && asset.ownership_revision > 0) actions.push({ label: '申请改名', run: () => showDatabaseRename(asset) })
  return actions
}
