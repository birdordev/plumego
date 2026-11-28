import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import { cancelPlan, readPlan } from './api'
import { MemberFields } from './MemberFields'
import { planLabels } from './schema'

const results = new Map(Object.entries({
  source_rejoin_pending: '源宿主机重新接入待审批', capacity_unavailable: '候选目标容量不足', storage_retry: '存储暂不可用，等待重试',
  authorization_changed: '申请人授权已变更', source_changed: '源宿主机已变更', vm_or_backup_changed: '虚拟机或备份已变更',
  verified_target_unavailable: '暂无具备备份校验条件的目标',
}))

export default function PlanReceipt({ target, admin, signal, stamp, back, refresh, openBatch }: {
  target: string; admin: boolean; signal: AbortSignal; stamp: number
  back: () => void; refresh: () => void; openBatch: (id: string) => void
}) {
  const result = useResourceRead(target, readPlan, signal)
  const [confirm, setConfirm] = useState(false)
  const { busy, run } = useDetailAction(stamp)
  const plan = result.state === 'ready' ? result.data : undefined
  const batch = plan?.batch_id
  return <>
    <div className="row-actions">
      <button type="button" onClick={back} disabled={busy}>返回疏散工作台</button>
      <button type="button" onClick={refresh} disabled={busy}>刷新预案回执</button>
      {admin && plan?.status === 'armed' && !confirm &&
        <button type="button" onClick={() => setConfirm(true)}>撤销疏散预案</button>}
      {batch && <button type="button" onClick={() => openBatch(batch)}>查看生成的疏散批次</button>}
    </div>
    {result.state === 'loading' && <p role="status">正在读取疏散预案…</p>}
    {result.state === 'error' && <p role="alert">{requestErrorMessage(result.error)}</p>}
    {plan && <>
      <DetailFields fields={[
        ['预案 ID', plan.plan_id], ['源宿主机', plan.source_host], ['登记版本', plan.host_revision], ['隔离代次', plan.source_fence_generation],
        ['状态', plan.status === 'armed' ? plan.activated_at ? '已激活，等待控制器' : '等待独立隔离确认' : planLabels[plan.status]],
        ['申请人', plan.requester], ['登记时间', timestamp(plan.armed_at)],
        ['激活时间', plan.activated_at && timestamp(plan.activated_at)], ['下次尝试', plan.next_attempt_at && timestamp(plan.next_attempt_at)],
        ['终止时间', plan.finished_at && timestamp(plan.finished_at)], ['撤销人', plan.cancelled_by],
        ['最近结果', plan.last_result && (results.get(plan.last_result) ?? plan.last_result)],
      ]} />
      {confirm && <section aria-label="确认撤销预案">
        <p>撤销后，这份预案将停止等待隔离确认或控制器重试。</p>
        <div className="row-actions">
          <button type="button" className="primary" disabled={busy} onClick={() => void run(() => cancelPlan(target, signal), refresh)}>确认撤销预案</button>
          <button type="button" autoFocus disabled={busy} onClick={() => setConfirm(false)}>返回预案</button>
        </div>
      </section>}
      <h3>登记的成员</h3>
      <ol className="evacuation-list">{plan.items.map((item) => <li key={item.vm_id}><MemberFields member={item} /></li>)}</ol>
    </>}
  </>
}
