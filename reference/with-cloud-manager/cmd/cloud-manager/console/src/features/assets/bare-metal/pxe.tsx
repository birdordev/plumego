import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

type Action = 'stage-bare-metal-pxe' | 'cancel-bare-metal-pxe'

export function showBareMetalPXE(asset: Asset, action: Action) {
  const target = projectURL() + '/bare-metal/pxe'
  const label = action === 'stage-bare-metal-pxe' ? '配置一次性 PXE' : '取消一次性 PXE'
  openDetail(label, (stamp) => <>
    <p>{asset.name} · {asset.account} / {asset.region} · {asset.id}</p>
    <p>归属版本 {asset.ownership_revision} · 需关机及独立审批。本申请仅修改启动配置。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交申请"
      payload={() => ({ account: asset.account, region: asset.region, asset_id: asset.id, action })} />
  </>)
}
