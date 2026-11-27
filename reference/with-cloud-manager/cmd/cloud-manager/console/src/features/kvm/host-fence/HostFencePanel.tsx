import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { sessionSnapshot } from '../../session/store'
import { hostStatus } from '../labels'
import { openMigrationReceipt } from '../live-migration/open'
import { openHostCleanups } from '../volume-cleanup/open'
import { readFence, readFenceWork } from './api'
import { FenceActionForm, type FenceIntent } from './FenceActionForm'
import { FenceEvents } from './FenceEvents'
import { FenceWorkList } from './FenceWorkList'
import type { FenceContext } from './open'
import { openRecovery } from './recovery/open'
import type { FenceHistory } from './schema'
import { availableFenceActions, labels, sameFenceSnapshot } from './state'

type Props = { context: FenceContext; stamp: number; signal: AbortSignal; refresh: () => void }

export default function HostFencePanel(props: Props) {
  const result = useResourceRead(props.context.target, readFence, props.signal)
  if (result.state === 'ready') return <FenceWorkspace {...props} history={result.data} />
  return <>
    {result.state === 'loading' ? <p role="status">正在读取隔离记录…</p> :
      <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    <div className="row-actions">
      <button type="button" onClick={props.context.backToHost}>返回宿主机</button>
      <button type="button" onClick={props.refresh}>刷新隔离工作台</button>
    </div>
  </>
}

function FenceWorkspace({ history, context, stamp, signal, refresh }: Props & { history: FenceHistory }) {
  const [intent, setIntent] = useState<FenceIntent>()
  const result = useResourceRead(context.target, readFenceWork, signal)
  const { host } = history
  const work = result.state === 'ready' ? result.data : undefined
  const snapshotChanged = work !== undefined && !sameFenceSnapshot(host, work)
  const clear = work && !snapshotChanged && work.items.length === 0 && !work.next && work.dispatched_volume_cleanups === 0
  const actions = availableFenceActions(history, context.role, sessionSnapshot()?.profile.principal)

  function cancel() {
    setDetailNotice(stamp)
    setIntent(undefined)
  }

  if (intent && !snapshotChanged) return <FenceActionForm intent={intent} history={history} target={context.target} stamp={stamp} signal={signal}
    cancel={cancel} onSaved={() => { void context.onChanged(); refresh() }} />

  return <>
    <DetailFields fields={[
      ['宿主机', host.id], ['站点', host.site], ['状态', hostStatus(host)], ['宿主机版本', host.revision],
      ['代理代次', host.generation], ['当前代理', host.agent],
    ]} />
    <div className="row-actions">
      <button type="button" onClick={context.backToHost}>返回宿主机</button>
      <button type="button" onClick={refresh}>刷新隔离工作台</button>
      {work && !snapshotChanged && work.dispatched_volume_cleanups > 0 && <button type="button" onClick={() => openHostCleanups({
        target: context.target, sitePath: context.sitePath, role: context.role, back: refresh, backLabel: '返回宿主机隔离',
      })}>查看阻塞的保留卷清理</button>}
      {!snapshotChanged && actions.map((action) => <button type="button" key={action}
        disabled={action === 'approve-rejoin' && !clear} onClick={() => setIntent({ action })}>{labels[action]}</button>)}
    </div>
    {actions.includes('approve-rejoin') && !clear && !snapshotChanged &&
      <p>清单确认无阻塞项后才能批准重新接入。</p>}
    {snapshotChanged && <p role="alert">宿主机版本或代次已变化，请刷新隔离工作台。</p>}
    {result.state === 'loading' && <p role="status">正在读取阻塞项…</p>}
    {result.state === 'error' && <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    {work && !snapshotChanged && host.fence_state && <FenceWorkList first={work} host={host} target={context.target} signal={signal}
      openVM={(id) => context.openVM(id, refresh)}
      openRecovery={(item) => openRecovery({ target: context.sitePath + '/vms/' + encodeURIComponent(item.vm_id),
        hostTarget: context.target, host, item, role: context.role, back: refresh })}
      openMigration={(vm, id) => openMigrationReceipt({ target: context.sitePath + '/vms/' + encodeURIComponent(vm),
        sitePath: context.sitePath, role: context.role, backLabel: '返回宿主机隔离' }, id, refresh)}
      onCleanup={context.role === 'approver' && (host.fence_state === 'fenced' || host.fence_state === 'rejoin_pending')
        ? (vm, migration) => setIntent({ action: 'cleanup', vm, migration }) : undefined} />}
    <FenceEvents events={history.events} />
  </>
}
