import { useState } from 'react'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import { InstanceLookup } from '../InstanceLookup'
import type { Asset } from '../types'

type Props = { asset: Asset; address: string; bind: boolean; target: string; stamp: number; signal: AbortSignal }

export function showElasticIPBinding(asset: Asset, bind: boolean) {
  const address = asset.public_addresses?.[0]
  if (!address) return
  const target = projectURL() + '/network-operations/' + (bind ? 'bind-elastic-ip' : 'unbind-elastic-ip')
  openDetail(bind ? '申请绑定弹性公网 IP' : '申请解绑弹性公网 IP', (stamp, signal) =>
    <BindingForm asset={asset} address={address} bind={bind} target={target} stamp={stamp} signal={signal} />)
}

function BindingForm({ asset, address, bind, target, stamp, signal }: Props) {
  const [instance, setInstance] = useState<Asset>()
  const requiresPort = asset.provider === 'huawei-ecs'
  return <>
    <p>{asset.account} / {asset.region} · {asset.id} · {address}</p>
    <p className="hint">{bind ? '绑定会为目标实例配置公网地址，提交后需独立审批。' : '解绑会断开该地址与实例的公网关联，地址仍保留在账号内。提交后需独立审批。'}</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel={bind ? '提交绑定申请' : '提交解绑申请'}
      payload={(data) => {
        if (!instance || instance.id !== formText(data, 'instance_id').trim()) throw new Error('请先核对实例。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, public_address: address,
          instance_id: instance.id, instance_revision: instance.ownership_revision,
          port_id: requiresPort ? formText(data, 'port_id') : undefined }
      }}>
      <InstanceLookup asset={asset} label={bind ? '目标实例 ID' : '当前绑定实例 ID'} stamp={stamp} signal={signal} onVerified={setInstance} />
      {requiresPort ? <label>网卡端口 ID<input name="port_id" required maxLength={36} pattern="[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}" /></label> : null}
    </OperationForm>
  </>
}
