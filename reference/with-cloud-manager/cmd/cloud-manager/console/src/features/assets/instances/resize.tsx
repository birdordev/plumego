import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { ListAssetActions } from '../row-action'
import type { Asset } from '../types'
import { readInstanceResizeOptions } from './resize-options'

const stoppedStates = new Map([['tencent-cvm', 'STOPPED'], ['alibaba-ecs', 'Stopped'], ['huawei-ecs', 'SHUTOFF']])

export const instanceResizeActions: ListAssetActions = (asset, { canExecute }) =>
  canExecute && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    asset.instance_type && stoppedStates.get(asset.provider) === asset.status
    ? [{ label: '申请变更规格', run: () => showInstanceResize(asset) }] : []

function showInstanceResize(asset: Asset) {
  const base = projectURL() + '/instances'
  openDetail('申请实例规格变更', (stamp, signal) => <ResizeOptionsPanel asset={asset} base={base} stamp={stamp} signal={signal} />)
}

type Props = { asset: Asset; base: string; stamp: number; signal: AbortSignal }

function ResizeOptionsPanel(props: Props) {
  const [requestNumber, setRequestNumber] = useState(0)
  return <ResizeOptionsResult key={requestNumber} {...props} refresh={() => setRequestNumber((number) => number + 1)} />
}

function ResizeOptionsResult({ asset, base, stamp, signal, refresh }: Props & { refresh: () => void }) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region, asset_id: asset.id })
  const result = useResourceRead(base + '/resize-options?' + query, readInstanceResizeOptions, signal)
  if (result.state === 'loading') return <p role="status">正在查询厂商可变配规格…</p>
  const retry = <button type="button" onClick={refresh}>重新查询</button>
  if (result.state === 'error') return <><p role="alert">{requestErrorMessage(result.error)}</p>{retry}</>
  const options = result.data
  if (options.current_type !== asset.instance_type || options.ownership_revision !== asset.ownership_revision) {
    return <p role="alert">实例规格或归属已经变化，请刷新资产列表。</p>
  }
  if (options.state === 'pending') return <><p role="status">控制器仍在查询厂商规格。</p>{retry}</>
  if (options.state === 'unavailable') return <><p role="status">暂未取得有效的厂商规格，请稍后重试。</p>{retry}</>
  if (!options.target_types.length) return <><p>当前没有可申请的目标规格。</p>{retry}</>
  const autoStart = asset.provider === 'tencent-cvm'
  return <>
    <p>{asset.account} / {asset.region} · {asset.id} · {options.zone}</p>
    <p>当前规格 {options.current_type} · 需独立审批</p>
    <OperationForm stamp={stamp} target={base + '/resize'} revision={options.ownership_revision} submitLabel="提交规格变更申请"
      payload={(data) => {
        const target = formText(data, 'target_type')
        if (!target || !options.target_types.includes(target)) throw new Error('请选择可申请的厂商规格。')
        if (!options.expires_at || Date.parse(options.expires_at) <= Date.now()) throw new Error('厂商规格已过期，请重新查询。')
        if (autoStart && !data.has('accept_auto_start')) throw new Error('请确认腾讯云变更后将启动实例。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, current_type: options.current_type,
          target_type: target, accept_auto_start: autoStart }
      }}>
      <label>目标规格<select name="target_type" defaultValue="" required>
        <option value="" disabled>请选择规格</option>
        {options.target_types.map((type) => <option key={type} value={type}>{type}</option>)}
      </select></label>
      {autoStart ? <label><input name="accept_auto_start" type="checkbox" required />同意腾讯云变更完成后启动实例</label> : null}
      {asset.provider === 'alibaba-ecs' ? <p className="hint">阿里云实例须使用普通停机模式。变更后的规格在后续开机时生效。</p> : null}
    </OperationForm>
    {retry}
  </>
}
