import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function databaseRecoveryFields(operation: OperationDetail): DetailField[] {
  const intent = operation.database_recovery_intent
  if (!intent) return []
  return [
    ['源数据库', intent.source_id], ['恢复时间', intent.restore_time],
    ['观察快照', intent.observed_at], ['目标名称', intent.name], ['目标数据库', intent.target_id],
    ['目标 VPC', intent.vpc_id], ['目标子网', intent.subnet_id],
    ...(intent.security_group_id ? [['目标安全组', intent.security_group_id] as DetailField] : []),
    ['目标可用区及规格', intent.zone + ' / ' + intent.flavor],
    ['目标存储', intent.storage_type + ' / ' + intent.storage_gib + ' GiB'],
  ]
}
