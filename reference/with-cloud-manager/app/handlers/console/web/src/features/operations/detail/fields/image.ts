import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function imageOperationFields(op: OperationDetail): DetailField[] {
  const fields: DetailField[] = [];
  if (op.image_copy_intent) {
    const info = op.image_copy_intent;
    fields.push(["源账号 / 地域", `${info.account} / ${info.region}`], ["源镜像 / 归属版本", `${info.asset_id} / ${info.source_revision}`],
      ["目标账号 / 地域", `${info.destination_account} / ${info.destination_region}`], ["目标镜像名称", info.name]);
  }
  if (op.image_creation_intent) {
    const { name, instance_id, instance_revision } = op.image_creation_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["镜像名称", name],
      ["源实例 / 归属版本", `${instance_id} / ${instance_revision}`]);
  }
  if (op.image_deletion_intent) {
    const { name, ownership_revision } = op.image_deletion_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["镜像名称", name], ["归属版本", ownership_revision]);
  }
  return fields;
}
