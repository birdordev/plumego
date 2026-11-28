import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

type Action = 'power-on-bare-metal' | 'shutdown-bare-metal'

export function showBareMetalPower(asset: Asset, action: Action) {
  const target = projectURL() + '/bare-metal/power'
  const label = action === 'power-on-bare-metal' ? '开机' : '优雅关机'
  openDetail('申请裸金属' + label, (stamp) => <>
    <p>{asset.name} · {asset.account} / {asset.region} · {asset.id}</p>
    <p>归属版本 {asset.ownership_revision} · 需启用账号电源权限及独立审批</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel={'提交' + label + '申请'}
      payload={() => ({ account: asset.account, region: asset.region, asset_id: asset.id, action })} />
  </>)
}
