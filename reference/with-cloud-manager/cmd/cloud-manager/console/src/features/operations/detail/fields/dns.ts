import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function dnsOperationFields(operation: OperationDetail): DetailField[] {
  if (operation.dns_record_creation_intent) {
    const { zone_name, name, type, value, ttl, record_id } = operation.dns_record_creation_intent;
    const fields: DetailField[] = [["托管区", zone_name], ["解析记录", name], ["类型 / 值", type + " / " + value], ["TTL", ttl + " 秒"]];
    if (record_id) fields.push(["厂商记录 ID", record_id]);
    return fields;
  }
  if (operation.dns_record_deletion_intent) {
    const { zone_name, record } = operation.dns_record_deletion_intent;
    const fields: DetailField[] = [["托管区", zone_name], ["待删除记录 / ID", record.name + " / " + record.id],
      ["当前状态", "已停用"], ["类型 / 值", record.type + " / " + (record.values?.join("、") ?? "—")],
      ["TTL", record.ttl + " 秒"]];
    if (record.line) fields.push(["线路", record.line]);
    return fields;
  }
  if (operation.dns_record_status_intent) {
    const { zone_name, name, record_id, current_status, enabled } = operation.dns_record_status_intent;
    return [["托管区", zone_name], ["解析记录 / ID", name + " / " + record_id],
      ["解析原状态", current_status], ["目标状态", enabled ? "启用解析" : "停用解析"]];
  }
  return [];
}
