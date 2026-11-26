import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const volumeStates: Partial<Record<string, string[]>> = {
  'tencent-cvm': ['UNATTACHED', 'ATTACHED'], 'alibaba-ecs': ['Available', 'In_use'], 'huawei-ecs': ['available', 'in-use'],
}
const deletableStates: Partial<Record<string, string>> = { 'tencent-cvm': 'NORMAL', 'alibaba-ecs': 'accomplished', 'huawei-ecs': 'available' }
const deletableClasses: Partial<Record<string, string[]>> = {
  'tencent-cvm': ['private_snapshot'], 'alibaba-ecs': ['standard', 'flash', 'archive'], 'huawei-ecs': ['standard', 'flash'],
}

export const diskNamePattern = '[a-zA-Z][a-zA-Z0-9_\\-]{1,63}'

export function canCreateSnapshot(asset: Asset) {
  return asset.kind === 'volume' && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    volumeStates[asset.provider]?.includes(asset.status)
}

export function canDeleteSnapshot(asset: Asset) {
  return asset.kind === 'snapshot' && asset.parent_id && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    deletableStates[asset.provider] === asset.status &&
    deletableClasses[asset.provider]?.includes(asset.snapshot?.class?.toLowerCase() ?? '')
}

export function showSnapshotCreation(asset: Asset) {
  const target = projectURL() + '/snapshots'
  openDetail('创建云盘快照', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">申请需独立审批。挂载中的卷支持崩溃一致性快照，应用可在提交前自行完成写入同步。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交快照申请"
      payload={(data) => ({ account: asset.account, region: asset.region, volume_id: asset.id,
        name: formText(data, 'name').trim(), allow_attached: data.has('allow_attached') })}>
      <label>快照名称<input name="name" required maxLength={64} pattern={diskNamePattern} /></label>
      <label><input type="checkbox" name="allow_attached" />允许在云盘挂载时创建崩溃一致性快照</label>
    </OperationForm>
  </>)
}

export function showSnapshotDeletion(asset: Asset) {
  const target = projectURL() + '/snapshots/delete'
  openDetail('申请删除云盘快照', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.name || asset.id} · {asset.id}</p>
    <p>源卷 {asset.parent_id} · 归属版本 {asset.ownership_revision} · {asset.snapshot?.class ?? '—'}</p>
    <p className="hint">删除后无法通过此快照恢复数据。提交申请后需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交删除申请"
      payload={() => ({ account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id })} />
  </>)
}
