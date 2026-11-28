import { useState, type ReactNode } from 'react'
import { requestErrorMessage } from '../../../../shared/api/requests'
import { useResourceRead } from '../../../../shared/api/useResourceRead'
import { openDetail } from '../../../../shared/detail/store'
import { request } from '../../../session/client'
import { AssetHistory, AssetTopology } from './Results'
import { assetHistoryPage, assetTopologyPage } from './schema'

const readHistory = (target: string, signal: AbortSignal) => request(target, { signal }, assetHistoryPage)
const readTopology = (target: string, signal: AbortSignal) => request(target, { signal }, assetTopologyPage)

export function showAssetHistory(base: string, filters: string) {
  openDetail('资产变化记录', (stamp, signal) => <EvidenceDrawer key={stamp} base={base + '/assets/changes'} filters={filters}
    signal={signal} read={readHistory} render={(page) => <AssetHistory page={page} />} />)
}

export function showAssetTopology(base: string, filters: string) {
  openDetail('资产拓扑', (stamp, signal) => <EvidenceDrawer key={stamp} base={base + '/assets/topology'} filters={filters}
    signal={signal} read={readTopology} render={(page) => <AssetTopology page={page} />} />)
}

type EvidencePage<T> = {
  signal: AbortSignal
  read: (target: string, signal: AbortSignal) => Promise<T>
  render: (page: T) => ReactNode
}

function EvidenceDrawer<T extends { next?: string }>({ base, filters, ...props }: EvidencePage<T> & { base: string; filters: string }) {
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const params = new URLSearchParams(filters)
  params.set('limit', '20')
  if (cursor) params.set('cursor', cursor)
  const target = base + '?' + params
  return <EvidenceContent key={target} target={target} {...props}
    previous={cursor ? () => setCursors((pages) => pages.slice(0, -1)) : undefined}
    next={(value) => setCursors((pages) => [...pages, value])} />
}

function EvidenceContent<T extends { next?: string }>({ target, signal, read, render, previous, next }: EvidencePage<T> & {
  target: string; previous?: () => void; next: (cursor: string) => void
}) {
  const result = useResourceRead(target, read, signal)
  if (result.state === 'loading') return <p role="status">正在读取…</p>
  if (result.state === 'error') return <p className="notice" role="alert" data-error="true">{requestErrorMessage(result.error)}</p>
  const cursor = result.data.next
  return <>
    {render(result.data)}
    {previous || cursor ? <div className="row-actions">
      {previous ? <button type="button" onClick={previous}>上一页</button> : null}
      {cursor ? <button type="button" onClick={() => next(cursor)}>下一页</button> : null}
    </div> : null}
  </>
}
