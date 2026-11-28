import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

export const networkDeletionLabels: Partial<Record<string, string>> = {
  network: '申请删除 VPC', subnet: '申请删除子网', 'security-group': '申请删除安全组',
}

export function canDeleteNetwork(asset: Asset) {
  return Object.hasOwn(networkDeletionLabels, asset.kind) && ['tencent-cvm', 'alibaba-ecs', 'huawei-ecs'].includes(asset.provider) &&
    asset.inventory_state === 'observed' && asset.ownership_revision > 0
}

export function showNetworkDeletion(asset: Asset) {
  const title = networkDeletionLabels[asset.kind]
  if (!title) return
  const securityGroup = asset.kind === 'security-group'
  const target = projectURL() + '/network-operations/delete'
  openDetail(title, (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.name || asset.id} · {asset.id}</p>
    <p>归属版本 {asset.ownership_revision}</p>
    <p className="hint">删除不可恢复。存在关联资源时会阻止删除，提交后需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交删除申请"
      payload={(data) => ({ kind: asset.kind, account: asset.account, region: asset.region, asset_id: asset.id,
        confirm_rule_removal: securityGroup ? data.has('confirm_rule_removal') : undefined })}>
      {securityGroup ? <label><input type="checkbox" name="confirm_rule_removal" required />确认同时删除安全组内的所有规则</label> : null}
    </OperationForm>
  </>)
}

export function canRevokeRule(asset: Asset) {
  return asset.kind === 'security-group-rule' && asset.parent_id && asset.inventory_state === 'observed'
}

export function showSecurityRuleRevocation(asset: Asset) {
  const target = projectURL() + '/network-operations/revoke-rule'
  openDetail('申请撤销安全组规则', (stamp) => <>
    <p>{asset.name}</p><p>{asset.account} / {asset.region} · {asset.parent_id} · {asset.id}</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交撤销申请"
      payload={() => ({ account: asset.account, region: asset.region, parent_id: asset.parent_id, rule_id: asset.id })} />
  </>)
}
