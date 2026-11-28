import type { DetailField } from '../../../../shared/components/DetailFields'
import { bucketEncryptionLabels, bucketVersioningLabels } from '../../../../shared/display/bucket-settings'
import type { OperationDetail } from '../schema'

export function bucketSettingFields(operation: OperationDetail): DetailField[] {
  const encryption = operation.bucket_encryption_intent
  if (encryption) return [['桶所在地域', encryption.location],
    ['当前默认加密', bucketEncryptionLabels[encryption.current_state]],
    ['目标默认加密', bucketEncryptionLabels[encryption.target_state]]]
  const change = operation.bucket_versioning_intent
  if (!change) return []
  return [['桶所在地域', change.location],
    ['当前版本控制', bucketVersioningLabels[change.current_state]],
    ['目标版本控制', bucketVersioningLabels[change.target_state]]]
}
