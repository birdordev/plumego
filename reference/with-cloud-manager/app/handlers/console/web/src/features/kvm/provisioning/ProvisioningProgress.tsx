import { useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import { readProvisioning, releasePlacement, reservePlacement, submitVM } from './api'
import { clearDraft, saveDraft } from './draft'
import type { Draft } from './schema'

type Props = {
  draft: Draft; storageKey: string; sitePath: string; stamp: number; signal: AbortSignal
  resume: (draft: Draft) => void; restart: () => void; openVM: (id: string) => void
}

export default function ProvisioningProgress(props: Props) {
  return props.draft.placement ? <PlacementProgress {...props} placement={props.draft.placement} /> : <ReserveProgress {...props} />
}

function DraftFields({ draft: { site, request, boot } }: { draft: Draft }) {
  return <DetailFields fields={[
    ['站点', site], ['宿主机', request.required_host], ['网络', request.network],
    ['存储池', request.shared_storage_pool_id ?? request.storage_pool],
    ['vCPU', request.vcpu], ['内存（MiB）', request.memory_mib], ['磁盘（MiB）', request.disk_mib],
    [boot.kind === 'image' ? '镜像' : '预置启动卷', boot.id], ['指定 IPv4', request.ipv4 || undefined],
  ]} />
}

function ReserveProgress({ draft, storageKey, sitePath, stamp, signal, resume, restart }: Props) {
  const { busy, run } = useDetailAction(stamp)
  const [started, setStarted] = useState(draft.attempted)
  const [resettable, setResettable] = useState(false)

  function reserve() {
    void run(async () => {
      const pending = { ...draft, attempted: true }
      saveDraft(storageKey, pending)
      setStarted(true)
      setResettable(false)
      try {
        const held = await reservePlacement(sitePath, pending.key, pending.request, signal)
        if (held.site !== draft.site || held.host !== draft.request.required_host) throw new Error('预留回执与原申请不符。')
        const updated = { ...pending, placement: held.id }
        saveDraft(storageKey, updated)
        return updated
      } catch (error) {
        if (error instanceof ConsoleRequestError && [400, 409, 413, 415].includes(error.status)) setResettable(true)
        throw error
      }
    }, resume)
  }

  return <>
    <DraftFields draft={draft} />
    <p>预留有效期为 5 分钟。预留后可提交创建审批或释放容量。</p>
    <div className="row-actions">
      <button type="button" className="primary" disabled={busy} onClick={reserve}>{started ? '重试原预留申请' : '预留容量'}</button>
      {(!started || resettable) && <button type="button" disabled={busy} onClick={() => void run(
        async () => clearDraft(storageKey, draft.key), restart,
      )}>返回重新配置</button>}
    </div>
  </>
}

const placementLabels = { held: '容量已预留', bound: '已绑定', expired: '预留已过期', released: '预留已释放' }

function PlacementProgress({ draft, placement, storageKey, sitePath, stamp, signal, resume, restart, openVM }: Props & { placement: string }) {
  const target = sitePath + '/placements/' + encodeURIComponent(placement)
  const result = useResourceRead(target, readProvisioning, signal)
  const { busy, run } = useDetailAction(stamp)
  const refresh = () => resume(draft)

  function finish(id: string) {
    void run(async () => { clearDraft(storageKey, draft.key); return id }, openVM)
  }

  if (result.state !== 'ready') return <>
    <p role="status">{result.state === 'error' ? '无法读取预留状态，请刷新后继续。' : '正在核对原预留和创建回执…'}</p>
    <button type="button" onClick={refresh}>刷新申请状态</button>
  </>
  const { placement: held, vm_id: vm } = result.data
  if (held.id !== placement || held.site !== draft.site) return <p role="alert">预留回执与原申请不符。</p>
  const available = held.state === 'held' && !vm

  return <>
    <DraftFields draft={draft} />
    <DetailFields fields={[
      ['放置 ID', held.id], ['状态', vm ? '已提交虚拟机创建' : placementLabels[held.state]],
      ['预留到期', timestamp(held.expires_at)], ['分配 IPv4', held.ipv4], ['MAC', held.mac], ['虚拟机 ID', vm],
    ]} />
    <div className="row-actions">
      <button type="button" disabled={busy} onClick={refresh}>刷新申请状态</button>
      {available && <>
        <button type="button" className="primary" disabled={busy} onClick={() => void run(async () => {
          const created = await submitVM(sitePath, placement, draft.boot, signal)
          clearDraft(storageKey, draft.key)
          return created.id
        }, openVM)}>提交创建审批</button>
        <button type="button" disabled={busy} onClick={() => void run(() => releasePlacement(target, signal), refresh)}>释放此预留</button>
      </>}
      {vm && <button type="button" className="primary" disabled={busy} onClick={() => finish(vm)}>打开已提交的虚拟机</button>}
      {!vm && (held.state === 'expired' || held.state === 'released') && <button type="button" disabled={busy} onClick={() => void run(
        async () => clearDraft(storageKey, draft.key), restart,
      )}>开始新的创建</button>}
    </div>
  </>
}
