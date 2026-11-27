import { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const storageClasses = new Map<string, readonly string[]>([
  ['tencent-cvm', ['CLOUD_PREMIUM', 'CLOUD_SSD', 'CLOUD_BSSD']],
  ['alibaba-ecs', ['cloud_essd', 'cloud_ssd', 'cloud_efficiency']],
  ['huawei-ecs', ['SAS', 'SSD', 'GPSSD']],
])

const namePattern = '[A-Za-z][A-Za-z0-9_\\-]{1,59}'
const volumeForm = z.object({
  name: z.string().regex(/^[A-Za-z][A-Za-z0-9_-]{1,59}$/),
  disk_type: z.string(),
  size_gib: z.string().regex(/^[0-9]+$/).transform(Number).pipe(z.number().int().min(10).max(32768)),
})

export function supportsVolumeCreation(zone: Asset) {
  const state = zone.status.toLowerCase()
  return zone.inventory_state === 'observed' && (state === 'available' || state === 'observed') &&
    storageClasses.has(zone.provider)
}

export function showVolumeCreation(zone: Asset) {
  const choices = storageClasses.get(zone.provider)
  if (!choices) return
  const target = projectURL() + '/volumes'
  const minimum = zone.provider === 'alibaba-ecs' ? 20 : 10

  openDetail('申请独立云盘', (stamp) => <>
    <p>{zone.account} / {zone.region} · {zone.id}</p>
    <p className="hint">创建单块按量数据盘，需独立审批。创建后可在资产列表查看容量和可用区。</p>
    <OperationForm stamp={stamp} target={target} submitLabel="提交云盘申请" payload={(data) => {
      const result = volumeForm.safeParse({
        name: formText(data, 'name'), disk_type: formText(data, 'disk_type'), size_gib: formText(data, 'size_gib'),
      })
      if (!result.success || !choices.includes(result.data.disk_type) || result.data.size_gib < minimum) {
        throw new Error('请检查名称、云盘类型及容量。')
      }
      return { ...result.data, account: zone.account, region: zone.region, zone: zone.id, encrypted: data.has('encrypted') }
    }}>
      <label>名称<input name="name" required maxLength={60} pattern={namePattern} /></label>
      <label>云盘类型<select name="disk_type" defaultValue="" required>
        <option value="">选择云盘类型</option>
        {choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}
      </select></label>
      <label>容量（GiB）<input name="size_gib" type="number" min={minimum} max={32768} step={1} required /></label>
      <label><input name="encrypted" type="checkbox" /> 要求云盘加密</label>
    </OperationForm>
  </>)
}
