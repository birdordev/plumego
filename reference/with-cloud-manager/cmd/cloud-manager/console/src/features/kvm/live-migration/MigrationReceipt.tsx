import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields, type DetailField } from '../../../shared/components/DetailFields'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import { sessionSnapshot, type Project } from '../../session/store'
import { readMigration, reviewMigration } from './api'
import { AbortMigrationForm } from './AbortMigrationForm'
import type { Migration } from './schema'
import { statuses } from './state'

type Props = {
  target: string; sitePath: string; role: Project['role']; stamp: number; signal: AbortSignal
  back: () => void; backLabel?: string; refresh: () => void
}

function receiptFields(item: Migration): DetailField[] {
  const fields: DetailField[] = [
    ['迁移 ID', item.migration_id], ['虚拟机', item.vm_id], ['状态', statuses[item.status]],
    ['源宿主机', item.source_host], ['目标宿主机', item.target_host],
    ['申请人', item.requester], ['申请时间', timestamp(item.requested_at)], ['审批人', item.reviewer],
    ['申请绑定的 VM 版本', item.vm_revision], ['迁移版本', item.revision],
  ]
  for (const [label, value] of [
    ['审批时间', item.reviewed_at], ['目标报告准备时间', item.prepared_at], ['派发时间', item.dispatched_at],
    ['源端离开时间', item.source_departed_at], ['完成时间', item.completed_at], ['终止时间', item.aborted_at],
  ] as const) {
    if (value) fields.push([label, timestamp(value)])
  }
  if (item.aborted_by) fields.push(['终止人', item.aborted_by], ['终止证据 SHA-256', item.abort_evidence_sha256])
  return fields
}

export default function MigrationReceipt({ target, sitePath, role, stamp, signal, back, backLabel = '返回迁移记录', refresh }: Props) {
  const result = useResourceRead(target, readMigration, signal)
  const [saved, setSaved] = useState<Migration>()
  const [action, setAction] = useState<'approve' | 'reject' | 'abort'>()
  const { busy, run } = useDetailAction(stamp)
  const item = saved ?? (result.state === 'ready' ? result.data : undefined)
  const reviewer = role === 'approver' && sessionSnapshot()?.profile.principal !== item?.requester

  function update(migration: Migration) {
    setSaved(migration)
    setAction(undefined)
  }

  return <>
    <div className="row-actions">
      <button type="button" disabled={busy || action === 'abort'} onClick={back}>{backLabel}</button>
      <button type="button" disabled={busy || action === 'abort'} onClick={refresh}>刷新迁移回执</button>
    </div>
    {result.state === 'loading' && <p role="status">正在加载迁移回执…</p>}
    {result.state === 'error' && <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    {item && <>
      <DetailFields fields={receiptFields(item)} />
      {action === 'abort' ? <AbortMigrationForm item={item} sitePath={sitePath} target={target} stamp={stamp} signal={signal}
        cancel={() => setAction(undefined)} onSaved={update} />
        : action ? <section aria-label="确认迁移审批">
          <p>{action === 'approve' ? '批准后将预留目标宿主机的计算资源。' : '拒绝后将关闭申请并释放已预留的目标计算资源。'}</p>
          <div className="row-actions">
            <button type="button" className="primary" disabled={busy} onClick={() => void run(
              () => reviewMigration(target, action, item.vm_revision, signal), update,
            )}>{action === 'approve' ? '确认批准迁移' : '确认拒绝迁移'}</button>
            <button type="button" autoFocus disabled={busy} onClick={() => setAction(undefined)}>取消</button>
          </div>
        </section> : reviewer && <div className="row-actions">
          {item.status === 'pending' && <button type="button" onClick={() => setAction('approve')}>批准迁移</button>}
          {(item.status === 'pending' || item.status === 'approved') && <button type="button" onClick={() => setAction('reject')}>拒绝迁移</button>}
          {(item.status === 'dispatched' || item.status === 'source_departed') &&
            <button type="button" onClick={() => setAction('abort')}>核对隔离并终止</button>}
        </div>}
    </>}
  </>
}
