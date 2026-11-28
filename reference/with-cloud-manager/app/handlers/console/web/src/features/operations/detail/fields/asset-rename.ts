import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function assetRenameFields(operation: OperationDetail): DetailField[] {
  const change = operation.database_rename_intent ?? operation.cache_rename_intent
  if (!change) return []
  return [
    [operation.cache_rename_intent ? '缓存产品' : '数据库产品', change.product],
    ['原名称', change.current_name || '未命名'],
    ['期望名称', change.name],
    ['归属版本', change.ownership_revision],
  ]
}
