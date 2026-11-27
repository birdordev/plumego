import { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

export const maxVolumeResizeGiB = 32768

export function showVolumeResize(asset: Asset, current: number) {
  const target = projectURL() + '/volumes/resize'
  const capacity = z.string().regex(/^[0-9]+$/).transform(Number)
    .pipe(z.number().int().min(current + 1).max(maxVolumeResizeGiB))
  openDetail('申请云盘扩容', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>当前容量 {current} GiB · 需独立审批</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交扩容申请"
      payload={(data) => {
        const result = capacity.safeParse(formText(data, 'target_gib'))
        if (!result.success) throw new Error(`请输入 ${current + 1}–${maxVolumeResizeGiB} GiB 的整数容量。`)
        return { account: asset.account, region: asset.region, asset_id: asset.id, current_gib: current, target_gib: result.data }
      }}>
      <label>目标容量（GiB）<input name="target_gib" type="number" min={current + 1} max={maxVolumeResizeGiB} step={1} required /></label>
      <p className="hint">支持独占、未挂载的按量数据盘。厂商会复核规格和限额，盘内分区及文件系统需单独扩展。</p>
    </OperationForm>
  </>)
}
