import { z } from 'zod'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { request } from '../../session/client'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const releasePreviewSchema = z.object({
  asset_id: z.string(),
  name: z.string(),
  size_gib: z.number().int().positive(),
  ownership_revision: z.number().int().positive(),
  data_disk: z.boolean(),
  attached_or_unavailable: z.boolean(),
  known_snapshots: z.boolean(),
  active_change: z.boolean(),
})

function readReleasePreview(target: string, signal: AbortSignal) {
  return request(target, { signal }, releasePreviewSchema)
}

export function showVolumeRelease(asset: Asset) {
  const base = projectURL() + '/volumes'
  openDetail('云盘释放预览', (stamp, signal) => <VolumeReleaseForm asset={asset} base={base} stamp={stamp} signal={signal} />)
}

function VolumeReleaseForm({ asset, base, stamp, signal }:
  { asset: Asset; base: string; stamp: number; signal: AbortSignal }) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region, asset_id: asset.id })
  const preview = useResourceRead(base + '/release-preview?' + query, readReleasePreview, signal)
  if (preview.state === 'loading') return <p role="status">正在核对云盘归属和依赖…</p>
  if (preview.state === 'error') return <p role="alert">{requestErrorMessage(preview.error)}</p>
  const info = preview.data
  if (info.asset_id !== asset.id) return <p role="alert">返回的云盘与所选资源不一致。</p>
  const blockers = [
    !info.data_disk && '需要已盘点的数据盘',
    info.attached_or_unavailable && '云盘已挂载或当前状态不可释放',
    info.known_snapshots && '云盘仍有关联快照',
    info.active_change && '存在同盘活动操作',
  ].filter(Boolean)
  return <>
    <p>{asset.account} / {asset.region} · {asset.id}</p>
    <p>{info.name || '未命名'} · {info.size_gib} GiB · 归属版本 {info.ownership_revision}</p>
    {blockers.length ? <ul aria-label="释放阻塞项">
      {blockers.map((blocker) => <li key={String(blocker)}>{blocker}</li>)}
    </ul> : <>
      <p className="hint">释放可能导致盘内数据无法恢复。请先备份。提交后需独立审批，派发前还会核对厂商快照和挂载状态。</p>
      <OperationForm stamp={stamp} target={base + '/release'} revision={info.ownership_revision} submitLabel="提交释放申请"
        payload={(data) => {
          const confirm_asset_id = formText(data, 'confirm_asset_id')
          if (confirm_asset_id !== asset.id) throw new Error('请输入完整云盘 ID 确认释放。')
          return { account: asset.account, region: asset.region, asset_id: asset.id, size_gib: info.size_gib, confirm_asset_id }
        }}>
        <label>确认云盘 ID<input name="confirm_asset_id" autoComplete="off" maxLength={128} required /></label>
      </OperationForm>
    </>}
  </>
}
