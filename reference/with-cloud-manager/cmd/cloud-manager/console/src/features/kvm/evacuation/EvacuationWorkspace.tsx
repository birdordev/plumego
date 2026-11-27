import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { readHost } from '../api'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { hostStatus } from '../labels'
import { listBatches, listPlans } from './api'
import { planLabels, type Mode, type Source } from './schema'
import './evacuation.css'

export default function EvacuationWorkspace({ target, signal, initialTab, back, refresh, start, restore, draftError, open }: {
  target: string; signal: AbortSignal; initialTab: Mode; back: () => void; refresh: (tab: Mode) => void
  start?: (source: Source) => void; restore?: () => void; draftError?: string; open: (mode: Mode, id: string) => void
}) {
  const [tab, setTab] = useState(initialTab)
  const result = useResourceRead(target, readHost, signal)
  const host = result.state === 'ready' ? result.data : undefined
  const mode: Mode | undefined = host?.fence_state === 'fenced' ? 'batch'
    : host && (!host.fence_state || host.fence_state === 'pending') ? 'plan' : undefined
  return <>
    {host && <DetailFields fields={[
      ['源宿主机', host.id], ['站点', host.site], ['当前状态', hostStatus(host)], ['当前宿主机版本', host.revision],
    ]} />}
    <div className="row-actions">
      <button type="button" onClick={back}>返回宿主机隔离</button>
      <button type="button" onClick={() => refresh(tab)}>刷新疏散工作台</button>
      {restore && <button type="button" onClick={restore}>恢复原申请</button>}
      {start && host && mode && <button type="button" className="primary" onClick={() => start({
        mode, host_revision: host.revision, source_fence_generation: host.generation + (host.fence_state ? 0 : 1),
      })}>{mode === 'plan' ? '登记疏散预案' : '申请疏散批次'}</button>}
    </div>
    {result.state === 'loading' && <p role="status">正在读取宿主机…</p>}
    {result.state === 'error' && <p role="alert">{requestErrorMessage(result.error)}</p>}
    {draftError && <p role="alert">{draftError}</p>}
    {start && host && !mode && <p>宿主机当前状态只允许查看已有疏散记录。</p>}
    <div className="row-actions" role="group" aria-label="疏散历史">
      <button type="button" aria-pressed={tab === 'batch'} onClick={() => setTab('batch')}>疏散批次</button>
      <button type="button" aria-pressed={tab === 'plan'} onClick={() => setTab('plan')}>疏散预案</button>
    </div>
    {tab === 'batch' ? <PagedResults key="batch" target={target} read={listBatches} signal={signal}>{(items) => items.length
      ? <ul className="evacuation-list">{items.map((item) => <li key={item.id}>
        <span>{timestamp(item.recorded_at)} · 代次 {item.source_fence_generation} · {item.requester}</span>
        <button type="button" onClick={() => open('batch', item.id)}>{item.id}</button>
      </li>)}</ul> : <p>暂无疏散批次。</p>}</PagedResults>
      : <PagedResults key="plan" target={target} read={listPlans} signal={signal}>{(items) => items.length
        ? <ul className="evacuation-list">{items.map((item) => <li key={item.id}>
          <span>{planLabels[item.status]} · {timestamp(item.recorded_at)} · 代次 {item.source_fence_generation} · {item.requester}</span>
          <button type="button" onClick={() => open('plan', item.id)}>{item.id}</button>
        </li>)}</ul> : <p>暂无疏散预案。</p>}</PagedResults>}
  </>
}
