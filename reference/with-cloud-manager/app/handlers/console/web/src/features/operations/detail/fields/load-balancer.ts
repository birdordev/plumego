import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function loadBalancerOperationFields(op: OperationDetail): DetailField[] {
  const fields: DetailField[] = [];
  if (op.load_balancer_backend_attach_intent) {
    const { target_group_id, instance_id, instance_revision, port, member_key, ownership_revision } = op.load_balancer_backend_attach_intent;
    fields.push(["后端组", target_group_id], ["源实例 / 归属版本", `${instance_id} / ${instance_revision}`], ["后端端口", port],
      ["初始权重", 0], ["负载均衡归属版本", ownership_revision]);
    if (member_key) fields.push(["成员标识", member_key]);
  }
  if (op.load_balancer_backend_detach_intent) {
    const { target_group_id, member_key, ownership_revision } = op.load_balancer_backend_detach_intent;
    fields.push(["后端组", target_group_id], ["成员标识", member_key], ["归属版本", ownership_revision]);
  }
  if (op.load_balancer_backend_weight_intent) {
    const { target_group_id, member_key, current_weight, weight, ownership_revision } = op.load_balancer_backend_weight_intent;
    fields.push(["后端组", target_group_id], ["成员标识", member_key], ["原权重", current_weight], ["新权重", weight], ["归属版本", ownership_revision]);
  }
  if (op.load_balancer_rename_intent) {
    const { product, listener_id, target_group_id, current_name, name, ownership_revision } = op.load_balancer_rename_intent;
    if (listener_id) fields.push(["监听器", listener_id]);
    if (target_group_id) fields.push(["后端组", target_group_id]);
    fields.push(["产品", product], ["原名称", current_name || "未命名"], ["新名称", name], ["归属版本", ownership_revision]);
  }
  return fields;
}
