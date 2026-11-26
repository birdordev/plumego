import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const namePattern = '[A-Za-z][A-Za-z0-9_\\-]{1,59}'

export function showLoadBalancerRename(asset: Asset, component: { id: string; name?: string } | null = null, isGroup = false) {
  const label = component ? (isGroup ? '后端组' : '监听器') : '负载均衡'
  const currentName = (component || asset).name || ''
  const suffix = component ? (isGroup ? 'rename-target-group' : 'rename-listener') : 'rename'
  const target = projectURL() + '/load-balancers/' + suffix
  openDetail('申请' + label + '改名', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>当前名称 {currentName || '未命名'} · 需独立审批</p>
    {component ? <p>{label} {component.id}</p> : null}
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交改名申请"
      payload={(data) => {
        const name = formText(data, 'name')
        if (name === currentName) throw new Error('请填写不同于当前名称的新名称。')
        return { account: asset.account, region: asset.region, asset_id: asset.id,
          ...(component && { [isGroup ? 'target_group_id' : 'listener_id']: component.id }), current_name: currentName, name }
      }}>
      <label>新名称<input name="name" required maxLength={60} pattern={namePattern} /></label>
    </OperationForm>
  </>)
}
