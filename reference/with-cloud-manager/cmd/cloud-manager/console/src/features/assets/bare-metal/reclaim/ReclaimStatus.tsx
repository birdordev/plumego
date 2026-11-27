import { DetailFields } from '../../../../shared/components/DetailFields'
import { timestamp } from '../../../../shared/display/time'
import type { Reclaim } from './schema'

const states = {
  requested: '待审批', approved: '等待站点领取', rejected: '已拒绝', claimed: '等待站点回报',
  verified: '凭证已验证，等待归属转移', consumed: '凭证已消费',
} satisfies Record<Reclaim['state'], string>

const recoveryStates = { requested: '待审批', rejected: '已拒绝', verified: '已验证' } as const

export function ReclaimStatus({ reclaim }: { reclaim: Reclaim }) {
  const { recovery } = reclaim
  return <>
    <DetailFields fields={[
      ['回收 ID', reclaim.id], ['回收状态', states[reclaim.state]], ['归属版本', reclaim.ownership_revision],
      ['清理服务主体', reclaim.sanitizer_id], ['申请人', reclaim.requester_id], ['审批人', reclaim.reviewer_id],
      ['申请时间', timestamp(reclaim.requested_at)], ['清理证据 SHA-256', reclaim.evidence_sha256],
    ]} />
    {recovery ? <section aria-label="恢复申请证据">
      <h3>恢复申请</h3>
      <DetailFields fields={[
        ['恢复 ID', recovery.id], ['恢复状态', recoveryStates[recovery.state]], ['恢复申请人', recovery.requester_id],
        ['恢复审批人', recovery.reviewer_id], ['隔离证明 SHA-256', recovery.isolation_sha256],
        ['清理证明 SHA-256', recovery.evidence_sha256],
      ]} />
    </section> : null}
  </>
}
