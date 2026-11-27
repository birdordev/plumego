import { useCallback, useState } from 'react'
import { requestErrorMessage } from '../../../../shared/api/requests'
import { useResourceRead } from '../../../../shared/api/useResourceRead'
import { DetailFields } from '../../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../../shared/detail/store'
import { useDetailAction } from '../../../../shared/detail/useDetailAction'
import { timestamp } from '../../../../shared/display/time'
import { sessionSnapshot } from '../../../session/store'
import { readHost, readVM } from '../../api'
import { vmStatus } from '../../labels'
import { readRecovery, reviewRecovery } from './api'
import type { RecoveryContext } from './open'
import { RecoveryRequestForm } from './RecoveryRequestForm'
import { observationLabels, observationsFor, type Recovery } from './schema'

type Props = { context: RecoveryContext; stamp: number; signal: AbortSignal; refresh: () => void }

const labels = { pending: '恢复待审批', approved: '恢复已批准', rejected: '恢复已拒绝' }

export default function RecoveryPanel({ context, stamp, signal, refresh }: Props) {
  const read = useCallback((target: string, requestSignal: AbortSignal) => Promise.all([
    readVM(target, requestSignal), readHost(context.hostTarget, requestSignal), readRecovery(target, requestSignal),
  ]), [context.hostTarget])
  const result = useResourceRead(context.target, read, signal)
  const [saved, setSaved] = useState<Recovery>()
  const [action, setAction] = useState<'request' | 'approve' | 'reject'>()
  const { busy, run } = useDetailAction(stamp)

  function choose(next?: typeof action) {
    setDetailNotice(stamp)
    setAction(next)
  }

  function showReceipt(receipt: Recovery) {
    setSaved(receipt)
    setAction(undefined)
  }

  const toolbar = <div className="row-actions">
    <button type="button" disabled={busy} onClick={context.back}>返回宿主机隔离</button>
    <button type="button" disabled={busy} onClick={refresh}>刷新恢复记录</button>
  </div>
  if (result.state !== 'ready') return <>
    {toolbar}
    {result.state === 'error' ? <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>
      : <p role="status">正在核对宿主机及虚拟机当前状态…</p>}
  </>

  const [vm, host, view] = result.data
  const { item } = context
  const receipt = saved ?? view.recovery
  const pending = receipt?.status === 'pending'
  const current = vm.id === item.vm_id && vm.host === host.id && vm.revision === item.vm_revision && vm.state === item.state &&
    vm.generation < host.generation && host.uuid === context.host.uuid && host.generation === context.host.generation &&
    host.revision === context.host.revision && host.fence_state === 'fenced' && observationsFor(vm.state).length > 0
  const canRequest = context.role === 'admin' && current && !pending && saved?.status !== 'approved'
  const canReject = context.role === 'approver' && receipt?.vm_id === vm.id && pending &&
    receipt.requester !== sessionSnapshot()?.profile.principal
  const canApprove = canReject && current && receipt.vm_revision === vm.revision && receipt.host_uuid === host.uuid &&
    receipt.fence_generation === host.generation && receipt.source_state === vm.state && observationsFor(vm.state).includes(receipt.observed)

  return <>
    <DetailFields fields={[
      ['虚拟机', vm.id], ['任务状态', vmStatus(saved?.result_state ?? vm.state)], ['原任务版本', item.vm_revision],
      ['宿主机', context.host.id], ['隔离代次', context.host.generation],
    ]} />
    {!action && toolbar}
    {!current && !saved && <p>批准或登记新证据需要当前隔离和任务版本。请回到宿主机清单重新核对。</p>}
    {receipt ? <DetailFields fields={[
      ['恢复 ID', receipt.recovery_id], ['恢复状态', labels[receipt.status]], ['现场观测', observationLabels[receipt.observed]],
      ['第一份证据 SHA-256', receipt.evidence_sha256], ['第二份独立证据 SHA-256', receipt.second_evidence_sha256],
      ['申请人', receipt.requester], ['申请时间', timestamp(receipt.requested_at)], ['审批人', receipt.reviewer],
      ['审批时间', timestamp(receipt.reviewed_at)], ['结果', receipt.result_state && vmStatus(receipt.result_state)],
    ]} /> : <p>暂无恢复申请。</p>}
    {action === 'request' && canRequest && <RecoveryRequestForm target={context.target} host={host} vm={vm} stamp={stamp}
      signal={signal} cancel={() => choose()} onSaved={showReceipt} />}
    {receipt && ((action === 'approve' && canApprove) || (action === 'reject' && canReject)) && <section aria-label="确认恢复审批">
      <p>{action === 'approve' ? '批准将按证据更新虚拟机任务状态和相应的容量、网络预留。' : '拒绝仅关闭此申请，原任务仍需处置。'}</p>
      <div className="row-actions">
        <button type="button" className="primary" disabled={busy} onClick={() => void run(
          () => reviewRecovery(context.target, receipt, action, signal), showReceipt,
        )}>{action === 'approve' ? '确认批准恢复' : '确认拒绝恢复'}</button>
        <button type="button" autoFocus disabled={busy} onClick={() => choose()}>取消</button>
      </div>
    </section>}
    {!action && <div className="row-actions">
      {canRequest && <button type="button" onClick={() => choose('request')}>登记恢复证据</button>}
      {canApprove && <button type="button" onClick={() => choose('approve')}>批准恢复</button>}
      {canReject && <button type="button" onClick={() => choose('reject')}>拒绝恢复</button>}
    </div>}
  </>
}
