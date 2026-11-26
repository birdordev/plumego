import { useState } from 'react'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import { InstanceLookup } from '../instances/InstanceLookup'
import type { Asset } from '../types'
import type { TargetGroup } from './schema'

type Props = { asset: Asset; group: TargetGroup; target: string; stamp: number; signal: AbortSignal }

export function showLoadBalancerBackendAttach(asset: Asset, group: TargetGroup) {
  const target = projectURL() + '/load-balancers/attach-backend'
  openDetail('申请添加后端成员', (stamp, signal) =>
    <AttachForm asset={asset} group={group} target={target} stamp={stamp} signal={signal} />)
}

function AttachForm({ asset, group, target, stamp, signal }: Props) {
  const [instance, setInstance] = useState<Asset>()
  return <>
    <p>{asset.account} / {asset.region} · {asset.id} · {group.name || group.id}</p>
    <p className="hint">新成员以 0 权重加入，申请需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交绑定申请"
      payload={(data) => {
        if (!instance || instance.id !== formText(data, 'instance_id').trim()) throw new Error('请先核对实例。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, target_group_id: group.id,
          instance_id: instance.id, instance_revision: instance.ownership_revision, port: Number(data.get('port')) }
      }}>
      <InstanceLookup asset={asset} label="后端实例 ID" stamp={stamp} signal={signal} onVerified={setInstance} />
      <label>后端端口<input name="port" type="number" min={1} max={65535} step={1} required /></label>
    </OperationForm>
  </>
}
