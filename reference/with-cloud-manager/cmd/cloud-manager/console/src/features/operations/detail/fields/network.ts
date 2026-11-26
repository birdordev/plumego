import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function networkOperationFields(op: OperationDetail): DetailField[] {
  const fields: DetailField[] = [];
  if (op.network_deletion_intent) {
    const { name, ownership_revision, parent_id, parent_revision, rule_removal_confirmed } = op.network_deletion_intent;
    fields.push(["账号 / 地域", op.account + " / " + op.region], ["资源名称", name], ["归属版本", ownership_revision]);
    if (parent_id) fields.push(["父 VPC / 归属版本", parent_id + " / " + parent_revision]);
    if (rule_removal_confirmed) fields.push(["规则删除", "已确认同时删除组内全部规则"]);
  }
  if (op.action === "create-subnet" && op.network_intent) {
    const { name, parent_id, parent_revision, cidr, zone } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["子网名称", name], ["父网络 / 归属版本", `${parent_id} / ${parent_revision}`],
      ["子网 CIDR", cidr], ["可用区", zone || "未指定"]);
  }
  if (op.action === "create-security-group" && op.network_intent) {
    const { name, parent_id, parent_revision } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组名称", name]);
    if (parent_id) fields.push(["父网络 / 归属版本", `${parent_id} / ${parent_revision}`]);
  }
  if (op.action === "create-elastic-ip" && op.network_intent) {
    const { name, bandwidth_mbps, address_pool } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
      ["带宽上限", `${bandwidth_mbps} Mbps`], ["计费方式", "按流量后付费"]);
    if (address_pool) fields.push(["地址池", address_pool]);
  }
  if (op.action === "release-elastic-ip" && op.network_intent) {
    const { name, asset_revision, public_address } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
      ["待释放地址", public_address], ["归属版本", asset_revision]);
  }
  if (["bind-elastic-ip", "unbind-elastic-ip"].includes(op.action) && op.network_intent) {
    const { name, asset_revision, public_address, parent_id, parent_revision, port_id } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
      ["公网地址 / 归属版本", `${public_address} / ${asset_revision}`], ["实例 / 归属版本", `${parent_id} / ${parent_revision}`]);
    if (port_id) fields.push(["网卡端口", port_id]);
  }
  if (op.network_intent?.rule) {
    const { parent_id, parent_revision, rule } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组 / 归属版本", `${parent_id} / ${parent_revision}`],
      ["规则方向", rule.direction === "ingress" ? "入站" : "出站"],
      ["协议 / 端口", `${rule.protocol.toUpperCase()} / ${rule.from_port}–${rule.to_port}`],
      ["远端 CIDR", rule.remote_cidr]);
  }
  if (op.action === "revoke-security-group-rule" && op.network_intent) {
    const { parent_id, parent_revision, name } = op.network_intent;
    fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组 / 归属版本", `${parent_id} / ${parent_revision}`], ["待撤销规则", name]);
  }
  return fields;
}
