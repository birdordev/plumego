import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { providerName } from '../../../shared/display/providers'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { ListAssetActions } from '../row-action'
import type { Asset } from '../types'
import { readRetirementPreview } from './retirement-api'

const stoppedStates = new Map([['tencent-cvm', 'stopped'], ['alibaba-ecs', 'stopped'], ['huawei-ecs', 'shutoff']])

export const instanceRetirementActions: ListAssetActions = (asset, { canExecute }) =>
  canExecute && asset.inventory_state === 'observed' && stoppedStates.get(asset.provider) === asset.status.toLowerCase() &&
  asset.ownership_revision > 0
    ? [{ label: '申请退役', run: () => showInstanceRetirement(asset) }]
    : []

function showInstanceRetirement(asset: Asset) {
  const base = projectURL() + '/instance-operations'
  openDetail('申请退役' + providerName(asset.provider), (stamp, signal) =>
    <RetirementForm asset={asset} base={base} stamp={stamp} signal={signal} />)
}

function RetirementForm({ asset, base, stamp, signal }: { asset: Asset; base: string; stamp: number; signal: AbortSignal }) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region, asset_id: asset.id })
  const preview = useResourceRead(base + '/retirement-preview?' + query, readRetirementPreview, signal)
  if (preview.state === 'loading') return <p role="status">正在核对当前归属和盘点系统盘…</p>
  if (preview.state === 'error') return <p role="alert">{requestErrorMessage(preview.error)}</p>
  const { system_disk_id, ownership_revision } = preview.data

  return <>
    <p>{asset.account} / {asset.region} · {asset.id}</p>
    <p>实例及其系统盘将永久释放。请先备份所需数据及快照。待核对的盘点磁盘为 {system_disk_id}，申请需独立审批。</p>
    <OperationForm stamp={stamp} target={base + '/retire'} revision={ownership_revision} submitLabel="提交退役申请"
      payload={(data) => {
        const confirm_asset_id = formText(data, 'confirm_asset_id')
        if (confirm_asset_id !== asset.id) throw new Error('请输入完整实例 ID 确认退役。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, system_disk_id, confirm_asset_id }
      }}>
      <label>确认实例 ID<input name="confirm_asset_id" autoComplete="off" required maxLength={128} /></label>
    </OperationForm>
  </>
}
