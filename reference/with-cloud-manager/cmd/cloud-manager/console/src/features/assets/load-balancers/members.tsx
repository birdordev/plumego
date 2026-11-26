import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'
import type { BackendMember } from './schema'

export function showLoadBalancerBackendWeight(asset: Asset, member: BackendMember) {
  const target = projectURL() + '/load-balancers/backend-weight'
  openDetail('申请修改后端权重', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>{member.group_id} · {member.key} · 端口 {member.port} · 当前权重 {member.weight}</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交权重申请"
      payload={(data) => {
        const weight = Number(data.get('weight'))
        if (weight === member.weight) throw new Error('请填写不同于当前权重的新权重。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, target_group_id: member.group_id,
          member_key: member.key, current_weight: member.weight, weight }
      }}>
      <label>新权重<input name="weight" type="number" min={0} max={100} step={1} defaultValue={member.weight} required /></label>
    </OperationForm>
  </>)
}

export function showLoadBalancerBackendDetach(asset: Asset, member: BackendMember) {
  const target = projectURL() + '/load-balancers/detach-backend'
  openDetail('申请解绑后端成员', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>{member.group_id} · {member.key} · 端口 {member.port}</p>
    <p>解绑前请确认现有连接已按业务要求排空。申请需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交解绑申请"
      payload={(data) => ({ account: asset.account, region: asset.region, asset_id: asset.id, target_group_id: member.group_id,
        member_key: member.key, confirm_detach: data.has('confirm_detach') })}>
      <label><input type="checkbox" name="confirm_detach" required />确认解绑该成员</label>
    </OperationForm>
  </>)
}
