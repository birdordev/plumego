import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function retirementFields(operation: OperationDetail): DetailField[] {
  const intent = operation.instance_retirement_intent
  if (!intent) return []
  return [
    ['待核对的系统盘 ID', intent.system_disk_id],
    ['系统盘归属版本', intent.disk_ownership_revision],
  ]
}
