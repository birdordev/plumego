import { useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { DetailFields } from '../../../shared/components/DetailFields'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { submitEvacuation } from './api'
import { clearDraft, saveDraft } from './draft'
import { MemberFields } from './MemberFields'
import type { Draft } from './schema'

export default function EvacuationSubmission({ target, keyName, draft, stamp, signal, back, onSaved }: {
  target: string; keyName: string; draft: Draft; stamp: number; signal: AbortSignal
  back: () => void; onSaved: (id: string) => void
}) {
  const [phase, setPhase] = useState(draft.phase)
  const { busy, run } = useDetailAction(stamp)
  function confirm() {
    void run(async () => {
      saveDraft(keyName, { ...draft, phase: 'sent' })
      setPhase('sent')
      try {
        const id = await submitEvacuation(target, draft, signal)
        if (id !== draft.id) throw new Error('服务返回的回执 ID 与原申请不符。')
        clearDraft(keyName, draft.id)
        return id
      } catch (error) {
        if (error instanceof ConsoleRequestError && [400, 409, 412, 413, 415].includes(error.status)) {
          saveDraft(keyName, { ...draft, phase: 'rejected' })
          setPhase('rejected')
        }
        throw error
      }
    }, onSaved)
  }
  return <>
    <DetailFields fields={[
      ['申请 ID', draft.id], ['源宿主机版本', draft.host_revision], ['目标隔离代次', draft.source_fence_generation],
      ['目标放置', '在其他故障域自动选址'],
    ]} />
    <ol className="evacuation-list">{draft.items.map((item) => <li key={item.vm_id}><MemberFields member={item} /></li>)}</ol>
    <p>{draft.mode === 'plan' ? '独立审批人确认宿主机隔离后，控制器才会生成批次。'
      : '所有成员的目标预留在同一事务中处理，申请受理后仍须逐台审批。'}</p>
    {phase === 'sent' && !busy && <p role="status">提交结果待确认。重试会沿用原 ID、版本和成员顺序。</p>}
    {phase === 'rejected' && <p role="status">服务已拒绝原申请。可以放弃草稿并重新选择。</p>}
    <div className="row-actions">
      {phase !== 'rejected' && <button type="button" disabled={busy} className="primary" onClick={confirm}>
        {phase === 'sent' ? '重试原申请' : draft.mode === 'plan' ? '确认登记疏散预案' : '确认申请疏散批次'}
      </button>}
      <button type="button" disabled={busy} onClick={back}>返回疏散工作台</button>
      {phase !== 'sent' && <button type="button" disabled={busy} onClick={() => void run(async () => {
        clearDraft(keyName, draft.id)
      }, back)}>放弃疏散草稿</button>}
    </div>
  </>
}
