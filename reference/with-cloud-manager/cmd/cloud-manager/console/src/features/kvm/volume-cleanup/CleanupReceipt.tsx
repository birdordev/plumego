import { useCallback, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields, type DetailField } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import { sessionSnapshot, type Project } from '../../session/store'
import { readHost } from '../api'
import { readCleanup, reviewCleanup } from './api'
import { CleanupResolution } from './CleanupResolution'
import { statuses, type Cleanup } from './schema'

type Props = {
  target: string; sitePath: string; role: Project['role']; stamp: number; signal: AbortSignal
  back: () => void; refresh: () => void
}

function receiptFields(job: Cleanup): DetailField[] {
  const fields: DetailField[] = [
    ['清理 ID', job.cleanup_id], ['状态', statuses[job.status]], ['虚拟机', job.vm_id],
    ['放置 ID', job.placement_id], ['原宿主机', job.host], ['原代理代次', job.host_generation],
    ['清理版本', job.revision], ['备份 ID', job.backup_image_id], ['保留策略证据 SHA-256', job.evidence_sha256],
    ['申请人', job.requester], ['申请时间', timestamp(job.requested_at)], ['审批人', job.reviewer],
  ]
  for (const [label, value] of [
    ['批准或拒绝时间', job.reviewed_at], ['派发时间', job.dispatched_at],
    ['首次确认缺失', job.first_absent_at], ['结束时间', job.finished_at],
  ] as const) {
    if (value) fields.push([label, timestamp(value)])
  }
  if (job.resolved_by) fields.push(['结算人', job.resolved_by], ['现场证据 SHA-256', job.resolution_sha256],
    ['第二份独立证据 SHA-256', job.second_resolution_sha256])
  return fields
}

export default function CleanupReceipt({ target, sitePath, role, stamp, signal, back, refresh }: Props) {
  const read = useCallback(async (path: string, requestSignal: AbortSignal) => {
    const job = await readCleanup(path, requestSignal)
    const host = await readHost(sitePath + '/hosts/' + encodeURIComponent(job.host), requestSignal)
    return { job, host }
  }, [sitePath])
  const result = useResourceRead(target, read, signal)
  const [saved, setSaved] = useState<Cleanup>()
  const [action, setAction] = useState<'approve' | 'reject' | 'resolve'>()
  const { busy, run } = useDetailAction(stamp)

  function update(job: Cleanup) {
    setSaved(job)
    setAction(undefined)
  }

  function choose(next?: typeof action) {
    setDetailNotice(stamp)
    setAction(next)
  }

  return <>
    {!action && <div className="row-actions">
      <button type="button" onClick={back}>返回清理列表</button>
      <button type="button" onClick={refresh}>刷新清理回执</button>
    </div>}
    {result.state === 'loading' && <p role="status">正在读取清理回执…</p>}
    {result.state === 'error' && <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    {result.state === 'ready' && renderReceipt(saved ?? result.data.job, result.data.host)}
  </>

  function renderReceipt(job: Cleanup, host: { generation: number; fence_state?: string }) {
    const principal = sessionSnapshot()?.profile.principal
    const reviewer = role === 'approver' && Boolean(principal) && principal !== job.requester
    const canApprove = job.status === 'pending' && host.generation === job.host_generation && !host.fence_state
    const canReject = job.status === 'pending' || job.status === 'approved'
    const canResolve = job.status === 'dispatched' && host.fence_state === 'fenced' && host.generation > job.host_generation
    return <>
      <DetailFields fields={receiptFields(job)} />
      {action === 'resolve' ? <CleanupResolution target={target} job={job} stamp={stamp} signal={signal}
        cancel={() => choose()} onSaved={update} /> : action ? <section aria-label="确认清理审批">
        <p>{action === 'approve' ? '批准后，绑定代理可以在核验备份及卷引用后领取删除授权。' : '拒绝后关闭清理申请并保留原卷。'}</p>
        <div className="row-actions">
          <button type="button" className="primary" disabled={busy} onClick={() => void run(
            () => reviewCleanup(target, job.revision, action, signal), update,
          )}>{action === 'approve' ? '确认批准清理' : '确认拒绝清理'}</button>
          <button type="button" autoFocus disabled={busy} onClick={() => choose()}>取消</button>
        </div>
      </section> : reviewer && <div className="row-actions">
        {canApprove && <button type="button" onClick={() => choose('approve')}>批准清理</button>}
        {canReject && <button type="button" onClick={() => choose('reject')}>拒绝清理</button>}
        {canResolve && <button type="button" onClick={() => choose('resolve')}>按现场证据结算</button>}
        {job.status === 'dispatched' && !canResolve && <p>现场结算需要原宿主机先确认进入新的隔离代次。</p>}
      </div>}
    </>
  }
}
