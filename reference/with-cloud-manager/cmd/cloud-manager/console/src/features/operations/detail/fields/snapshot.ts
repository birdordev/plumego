import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function snapshotOperationFields(op: OperationDetail): DetailField[] {
  const fields: DetailField[] = [];
  if (op.snapshot_restore_intent) {
    const info = op.snapshot_restore_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["源快照 / 类型", `${info.snapshot_id} / ${info.snapshot_class}`],
      ["源卷 / 归属版本", `${info.source_volume_id} / ${info.source_volume_revision}`], ["新云盘名称", info.name],
      ["可用区 / 盘型", `${info.zone} / ${info.disk_type}`], ["容量", `${info.size_gib} GiB`],
      ["源快照加密", info.encrypted ? "是" : "否"], ["计费方式", "按需"]);
  }
  if (op.snapshot_intent) {
    const { name, volume_id, volume_revision, allow_attached, class: snapshotClass } = op.snapshot_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["快照名称", name],
      ["源卷 / 归属版本", `${volume_id} / ${volume_revision}`]);
    if (["delete-snapshot", "rollback-snapshot"].includes(op.action)) fields.push(["快照类别", snapshotClass]);
    else fields.push(["允许使用挂载中的源卷", allow_attached ? "是，崩溃一致性" : "否"]);
  }
  return fields;
}
