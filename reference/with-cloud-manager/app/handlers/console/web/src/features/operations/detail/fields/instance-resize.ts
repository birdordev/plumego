import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function instanceResizeFields(operation: OperationDetail): DetailField[] {
  const change = operation.instance_resize_intent
  if (!change) return []
  return [
    ['当前规格', change.current_type], ['目标规格', change.target_type],
    ['同意厂商自动启动', change.accept_auto_start ? '是' : '否'],
    ['实例归属版本', change.ownership_revision.toString()],
  ]
}
