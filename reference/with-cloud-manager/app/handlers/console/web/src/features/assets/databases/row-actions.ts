import type { ListAssetActions } from '../row-action'
import { showBackups } from './backups'
import { showDatabaseRename } from './rename'
import { showRestoreWindows } from './restore-windows'

const productsByProvider = new Map<string, readonly string[]>([
  ['tencent-cvm', ['tencent-cdb', 'tencent-postgresql', 'tencent-sqlserver']],
  ['alibaba-ecs', ['alibaba-rds']],
  ['huawei-ecs', ['huawei-rds']],
])

const restoreEngines = new Map<string, readonly string[]>([
  ['tencent-cdb', ['mysql']],
  ['tencent-postgresql', ['postgresql']],
  ['tencent-sqlserver', ['sqlserver']],
  ['alibaba-rds', ['mysql', 'postgresql', 'mariadb']],
  ['huawei-rds', ['mysql', 'postgresql', 'sqlserver']],
])

export const databaseActions: ListAssetActions = (asset, { canExecute }) => {
  const database = asset.database_instance
  if (asset.inventory_state !== 'observed' || !database || !productsByProvider.get(asset.provider)?.includes(database.product)) return []
  const actions = [{ label: '查看备份', run: () => showBackups(asset) }]
  if (restoreEngines.get(database.product)?.includes(database.engine)) {
    actions.push({ label: '查看可恢复时间', run: () => showRestoreWindows(asset, canExecute) })
  }
  if (canExecute && asset.ownership_revision > 0) actions.push({ label: '申请改名', run: () => showDatabaseRename(asset) })
  return actions
}
