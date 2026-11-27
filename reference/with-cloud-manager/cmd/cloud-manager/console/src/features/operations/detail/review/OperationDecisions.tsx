import { useRef } from 'react'
import { setDetailNotice } from '../../../../shared/detail/store'
import { useDetailAction } from '../../../../shared/detail/useDetailAction'
import { request } from '../../../session/client'
import { operationsCollection } from '../../history/collection'
import { reviewChoices } from './review-policy'
import type { OperationDetail } from '../schema'

type Props = { operation: OperationDetail; target: string; stamp: number; approvalReady?: boolean; onSaved: () => void }

export function OperationDecisions({ operation, target, stamp, approvalReady = true, onSaved }: Props) {
  const { busy, run } = useDetailAction(stamp)
  const evidence = useRef<HTMLInputElement>(null)
  const record = useRef<HTMLInputElement>(null)
  const deployment = operation.bare_metal_deployment?.deployment

  function send(suffix: string, payload: Record<string, string | boolean | undefined>) {
    void run(() => request(target + '/' + suffix, { method: 'POST', body: JSON.stringify(payload) }), () => {
      onSaved()
      void operationsCollection.refresh()
    })
  }

  if (operation.status === 'awaiting_approval') return <>
    <div className="row-actions">
      <button type="button" disabled={busy || !approvalReady || (deployment != null && deployment.state !== 'pending')} onClick={() => send('approval', {
        decision: 'approve', deployment_id: deployment?.id,
      })}>批准</button>
      <button type="button" disabled={busy} onClick={() => void send('approval', { decision: 'reject' })}>拒绝</button>
      {operation.action === 'power-on-bare-metal' && !deployment ?
        <button type="button" disabled={busy} onClick={onSaved}>刷新开机详情</button> : null}
    </div>
  </>
  const { observe, succeed, needsRecordID } = reviewChoices(operation)
  const repairQueue = Boolean(operation.queue_issue_reason) && observe
  if (operation.status !== 'needs_review' && !repairQueue) return null

  function review(decision?: string) {
    const providerID = record.current?.value.trim()
    if (!decision && needsRecordID && !providerID) return setDetailNotice(stamp, '请填写已核实的厂商记录 ID。', true)
    const evidenceRef = evidence.current?.value.trim()
    if (!evidenceRef) return setDetailNotice(stamp, '请填写证据引用。', true)
    if (decision) void send('resolution', { decision, evidence_ref: evidenceRef, provider_request_id: operation.provider_request_id })
    else void send('retry-observation', {
      evidence_ref: evidenceRef, provider_resource_id: providerID, repair_queue: repairQueue || undefined,
    })
  }

  return <>
    <label>证据引用<input ref={evidence} maxLength={200} /></label>
    {needsRecordID ? <label>经厂商审计核实的记录 ID<input ref={record} maxLength={128} /></label> : null}
    <div className="row-actions">
      {repairQueue ? <button type="button" disabled={busy} onClick={() => review()}>修复队列并恢复只读观测</button> : <>
        <button type="button" disabled={busy} onClick={() => review('failed')}>记录失败</button>
        {observe ? <button type="button" disabled={busy} onClick={() => review()}>恢复只读观测</button> : null}
        {succeed ? <button type="button" disabled={busy} onClick={() => review('succeeded')}>记录成功</button> : null}
      </>}
    </div>
  </>
}
