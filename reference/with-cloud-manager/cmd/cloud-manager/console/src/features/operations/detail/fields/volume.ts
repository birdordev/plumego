import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function volumeOperationFields(operation: OperationDetail): DetailField[] {
  const creation = operation.volume_creation_intent
  if (creation) return [
    ['名称', creation.name], ['可用区', creation.zone], ['云盘类型', creation.disk_type],
    ['容量', `${creation.size_gib} GiB`], ['加密要求', creation.encrypted ? '需要' : '采用账号默认策略'],
  ]
  const release = operation.volume_release_intent
  if (release) return [
    ['名称', release.name || '未命名'], ['原容量', `${release.size_gib} GiB`], ['归属版本', String(release.ownership_revision)],
  ]
  const change = operation.volume_resize_intent
  if (!change) return []
  return [
    ['原容量', `${change.current_gib} GiB`],
    ['目标容量', `${change.target_gib} GiB`],
    ['归属版本', String(change.ownership_revision)],
  ]
}
