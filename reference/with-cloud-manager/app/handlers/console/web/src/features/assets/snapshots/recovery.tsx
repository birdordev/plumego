import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'
import { diskNamePattern } from './actions'

export const restoreDiskTypes: Partial<Record<string, string[]>> = {
  'tencent-cvm': ['CLOUD_PREMIUM', 'CLOUD_SSD', 'CLOUD_BSSD'],
  'alibaba-ecs': ['cloud_essd', 'cloud_ssd', 'cloud_efficiency'],
  'huawei-ecs': ['GPSSD', 'SSD', 'SAS'],
}

export function showSnapshotRollback(asset: Asset) {
  const target = projectURL() + '/snapshots/rollback'
  openDetail('申请回滚原云盘', (stamp) => <>
    <p>{asset.account} / {asset.region} · 快照 {asset.id} · 原卷 {asset.parent_id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">回滚会覆盖原云盘上快照之后的全部写入。原卷须为未挂载的数据盘，提交后需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交回滚申请"
      payload={(data) => ({ account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id,
        confirm_overwrite: data.has('confirm_overwrite') })}>
      <label><input type="checkbox" name="confirm_overwrite" required />确认覆盖原云盘上的数据</label>
    </OperationForm>
  </>)
}

export function showSnapshotRestore(asset: Asset) {
  const target = projectURL() + '/snapshots/restore'
  const minimum = Math.max(10, asset.snapshot?.size_gib || 0)
  openDetail('从快照恢复到新盘', (stamp) => <>
    <p>{asset.account} / {asset.region} · 快照 {asset.id} · 源卷 {asset.parent_id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">恢复将创建一块独立的按需计费数据盘。提交后需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交恢复申请"
      payload={(data) => ({
        account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id,
        name: formText(data, 'name').trim(), zone: formText(data, 'zone').trim(), disk_type: formText(data, 'disk_type'),
        size_gib: Number(data.get('size_gib')), encrypted: data.has('encrypted'),
      })}>
      <label>新云盘名称<input name="name" required maxLength={64} pattern={diskNamePattern} /></label>
      <label>目标可用区<input name="zone" required maxLength={64} /></label>
      <label>容量（GiB）<input name="size_gib" type="number" min={minimum} max={32768} defaultValue={minimum} required /></label>
      <label>目标盘型<select name="disk_type" required defaultValue="">
        <option value="">请选择盘型</option>
        {restoreDiskTypes[asset.provider]?.map((type) => <option key={type}>{type}</option>)}
      </select></label>
      <label><input type="checkbox" name="encrypted" />源快照已加密</label>
    </OperationForm>
  </>)
}
