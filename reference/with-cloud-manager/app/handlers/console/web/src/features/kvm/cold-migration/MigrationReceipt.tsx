import { useCallback, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import { sessionSnapshot } from '../../session/store'
import { readPlacement, releasePlacement } from '../provisioning/api'
import { readColdReceipt, reviewColdMigration } from './api'
import type { ReceiptContext } from './open'
import { statuses, type Receipt } from './schema'

type Props = { context: ReceiptContext; id: string; stamp: number; signal: AbortSignal; back: () => void; backLabel: string; refresh: () => void }

export default function MigrationReceipt({ context, id, stamp, signal, back, backLabel, refresh }: Props) {
  const read = useCallback(async (target: string, requestSignal: AbortSignal) => {
    const receipt = await readColdReceipt(target, requestSignal)
    const placementPath = context.sitePath + '/placements/' + encodeURIComponent(receipt.target_placement_id)
    return { receipt, placement: await readPlacement(placementPath, requestSignal), placementPath }
  }, [context.sitePath])
  const result = useResourceRead(context.target + '/cold-migrations/' + encodeURIComponent(id), read, signal)
  const [saved, setSaved] = useState<Receipt>()
  const [decision, setDecision] = useState<'approve' | 'reject'>()
  const { busy, run } = useDetailAction(stamp)

  function choose(next?: typeof decision) {
    setDetailNotice(stamp)
    setDecision(next)
  }
  const toolbar = <div className="row-actions">
    <button type="button" onClick={back} disabled={busy}>{backLabel}</button>
    <button type="button" onClick={refresh} disabled={busy}>刷新迁移回执</button>
  </div>
  if (result.state !== 'ready') return <>
    {toolbar}
    {result.state === 'error' ? <p role="alert">{requestErrorMessage(result.error)}</p> : <p role="status">正在核对目标预留…</p>}
  </>
  const { placement, placementPath } = result.data
  const item = saved ?? result.data.receipt
  const principal = sessionSnapshot()?.profile.principal
  const canReview = context.role === 'approver' && principal && principal !== item.requester && item.status === 'pending'
  const canRelease = context.role === 'admin' && principal === item.requester && item.status === 'rejected' &&
    !item.automatic_placement && placement.state === 'held'
  return <>
    {!decision && toolbar}
    <DetailFields fields={[
      ['迁移 ID', item.migration_id], ['状态', statuses[item.status]], ['虚拟机', item.vm_id],
      ['源宿主机', item.source_host], ['源放置', item.source_placement_id], ['隔离代次', item.source_fence_generation],
      ['目标宿主机', item.target_host], ['目标放置', item.target_placement_id], ['预留到期', timestamp(placement.expires_at)],
      ['源 VM 版本', item.vm_revision], ['备份镜像', item.image_id], ['要求有效备份回执', item.require_verified_backup ? '是' : '否'],
      ['现场证据 SHA-256', item.evidence_sha256], ['申请人', item.requester], ['申请时间', timestamp(item.requested_at)],
      ['审批人', item.reviewer], ['审批时间', item.reviewed_at ? timestamp(item.reviewed_at) : undefined],
      ['源端清理时间', item.cleanup_at ? timestamp(item.cleanup_at) : undefined], ['源端清理证据', item.cleanup_sha256],
    ]} />
    {item.status === 'pending' && placement.state !== 'held' && <p>目标预留已不可用。请拒绝原申请后重新提交。</p>}
    {decision ? <section aria-label="确认迁移审批">
      <p>{decision === 'approve' ? '批准将转移虚拟机归属和地址，并为目标宿主机排队。' : '拒绝将结束本次迁移申请。'}</p>
      <div className="row-actions">
        <button type="button" className="primary" disabled={busy} onClick={() => void run(
          () => reviewColdMigration(context.target, item, decision, signal), (updated) => {
            setSaved(updated)
            setDecision(undefined)
          },
        )}>{decision === 'approve' ? '确认批准迁移' : '确认拒绝迁移'}</button>
        <button type="button" autoFocus disabled={busy} onClick={() => choose()}>取消</button>
      </div>
    </section> : <div className="row-actions">
      {canReview && placement.state === 'held' && <button type="button" onClick={() => choose('approve')}>批准迁移</button>}
      {canReview && <button type="button" onClick={() => choose('reject')}>拒绝迁移</button>}
      {canRelease && <button type="button" disabled={busy} onClick={() => void run(
        () => releasePlacement(placementPath, signal), refresh,
      )}>释放已拒绝的目标预留</button>}
    </div>}
  </>
}
