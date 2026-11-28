import type { AssetRowAction, ListAssetActions } from '../row-action'
import { canCreateSnapshot, canDeleteSnapshot, showSnapshotCreation, showSnapshotDeletion } from './actions'
import { showSnapshotRestore, showSnapshotRollback } from './recovery'

export const snapshotCreationActions: ListAssetActions = (asset, { canExecute }) => canExecute && canCreateSnapshot(asset)
  ? [{ label: '创建快照', run: () => showSnapshotCreation(asset) }]
  : []

export const snapshotActions: ListAssetActions = (asset, { canExecute }) => {
  if (!canExecute || !canDeleteSnapshot(asset)) return []
  const actions: AssetRowAction[] = []
  if (asset.snapshot?.class?.toLowerCase() !== 'archive') actions.push(
    { label: '恢复到新盘', run: () => showSnapshotRestore(asset) },
    { label: '回滚原云盘', run: () => showSnapshotRollback(asset) },
  )
  actions.push({ label: '申请删除', run: () => showSnapshotDeletion(asset) })
  return actions
}
