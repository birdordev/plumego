import type { DetailField } from '../../../../shared/components/DetailFields'
import { bucketVersioningLabels } from '../../../../shared/display/bucket-settings'
import type { OperationDetail } from '../schema'

export function bucketVersioningFields(operation: OperationDetail): DetailField[] {
  const change = operation.bucket_versioning_intent
  if (!change) return []
  return [['桶所在地域', change.location],
    ['当前版本控制', bucketVersioningLabels[change.current_state]],
    ['目标版本控制', bucketVersioningLabels[change.target_state]]]
}
