import { lazy, Suspense } from 'react'
import { openDetail } from '../../../../shared/detail/store'
import type { Project } from '../../../session/store'
import type { HostDetail } from '../../schema'
import type { WorkItem } from '../schema'

const RecoveryPanel = lazy(() => import('./RecoveryPanel'))

export type RecoveryContext = {
  target: string; hostTarget: string; host: HostDetail; item: WorkItem; role: Project['role']; back: () => void
}

export function openRecovery(context: RecoveryContext) {
  openDetail('隔离虚拟机恢复', (stamp, signal) => <Suspense fallback={<p role="status">正在读取恢复证据…</p>}>
    <RecoveryPanel context={context} stamp={stamp} signal={signal} refresh={() => openRecovery(context)} />
  </Suspense>)
}
