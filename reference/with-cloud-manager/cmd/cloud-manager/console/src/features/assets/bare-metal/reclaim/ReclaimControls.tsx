import type { Session } from '../../../session/store'
import type { Asset } from '../../types'
import { reviewReclaim, reviewRecovery } from './api'
import { ReclaimRequestForm } from './ReclaimRequestForm'
import { RecoveryRequestForm } from './RecoveryRequestForm'
import { ReclaimDecisionForm } from './ReclaimDecisionForm'
import { ReclaimTransferForm } from './ReclaimTransferForm'
import type { Reclaim, TransferredOwnership } from './schema'

type Props = {
  asset: Asset; project: string; session: Session; stamp: number; reclaim: Reclaim | null
  onSaved: (value: Reclaim) => void; onTransfer: (value: TransferredOwnership) => void
}

export function ReclaimControls({ asset, project, session, stamp, reclaim, onSaved, onTransfer }: Props) {
  const { role } = session.project
  const { principal, projects } = session.profile
  if (!reclaim || reclaim.state === 'rejected') {
    if (role !== 'admin') return null
    return asset.status === 'off' ?
      <ReclaimRequestForm project={project} asset={asset} stamp={stamp} onSaved={onSaved} /> :
      <p>申请回收前请先完成关机和站点盘点。</p>
  }

  const recovery = reclaim.recovery
  const canReview = role === 'approver' && principal !== reclaim.requester_id && principal !== reclaim.sanitizer_id
  if (canReview && (reclaim.state === 'requested' || reclaim.state === 'approved')) {
    return <ReclaimDecisionForm stamp={stamp} label="提交回收审批" canApprove={reclaim.state === 'requested'}
      submit={(decision) => reviewReclaim(project, reclaim.id, decision)} onSaved={onSaved} />
  }
  if (reclaim.state === 'claimed') {
    if (recovery?.state === 'requested') {
      return canReview && principal !== recovery.requester_id ?
        <ReclaimDecisionForm stamp={stamp} label="提交恢复审批" canApprove
          submit={(decision) => reviewRecovery(project, reclaim.id, recovery.id, decision)} onSaved={onSaved} /> : null
    }
    return role === 'admin' && principal !== reclaim.sanitizer_id ?
      <RecoveryRequestForm stamp={stamp} project={project} reclaim={reclaim} systemID={asset.id} onSaved={onSaved} /> : null
  }
  if (role === 'admin' && reclaim.state === 'verified') {
    const destinations = projects.filter((p) => p.role === 'admin' && p.id !== session.project.id)
    return <ReclaimTransferForm project={project} asset={asset} reclaim={reclaim} destinations={destinations}
      stamp={stamp} onSaved={onTransfer} />
  }
  return null
}
