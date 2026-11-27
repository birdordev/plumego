import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { openDetail } from '../../../shared/detail/store'
import { timestamp } from '../../../shared/display/time'
import { request } from '../../session/client'
import { BacklogTable } from './BacklogTable'
import { backlogSchema } from './schema'

const readBacklog = (target: string, signal: AbortSignal) => request(target, { signal }, backlogSchema)

export function showOperationBacklog(base: string, filters: string) {
  const current = new URLSearchParams(filters)
  const query = new URLSearchParams()
  for (const name of ['account', 'region']) {
    const value = current.get(name)
    if (value) query.set(name, value)
  }
  const target = base + '/operation-backlog' + (query.size ? '?' + query : '')
  openDetail('操作积压', (stamp, signal) => <BacklogDrawer key={stamp} target={target}
    signal={signal} account={query.get('account')} region={query.get('region')} />)
}

function BacklogDrawer({ target, signal, account, region }: {
  target: string; signal: AbortSignal; account: string | null; region: string | null
}) {
  const [refresh, setRefresh] = useState(0)
  const scope = [account && `账号 ${account}`, region && `地域 ${region}`].filter(Boolean).join('，')
  return <>
    <p>{scope || '项目全部账号和地域'}，全部非终态操作。</p>
    <BacklogContent key={refresh} target={target} signal={signal} onRefresh={() => setRefresh((value) => value + 1)} />
  </>
}

function BacklogContent({ target, signal, onRefresh }: { target: string; signal: AbortSignal; onRefresh: () => void }) {
  const result = useResourceRead(target, readBacklog, signal)
  if (result.state === 'loading') return <p role="status">正在读取操作积压…</p>
  if (result.state === 'error') return <>
    <p className="notice" data-error="true" role="alert">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={onRefresh}>重试</button>
  </>
  return <>
    <div className="row-actions">
      <p>观测时间 {timestamp(result.data.as_of)}</p>
      <button type="button" onClick={onRefresh}>刷新快照</button>
    </div>
    <BacklogTable report={result.data} />
  </>
}
