import type { ListAssetActions } from '../row-action'
import { showDatabaseRename } from './rename'

const productsByProvider = new Map<string, readonly string[]>([
  ['tencent-cvm', ['tencent-cdb', 'tencent-postgresql', 'tencent-sqlserver']],
  ['alibaba-ecs', ['alibaba-rds']],
  ['huawei-ecs', ['huawei-rds']],
])

export const databaseActions: ListAssetActions = (asset, { canExecute }) => {
  const product = asset.database_instance?.product
  if (!canExecute || asset.inventory_state !== 'observed' || asset.ownership_revision <= 0 ||
    !product || !productsByProvider.get(asset.provider)?.includes(product)) return []
  return [{ label: '申请改名', run: () => showDatabaseRename(asset) }]
}
