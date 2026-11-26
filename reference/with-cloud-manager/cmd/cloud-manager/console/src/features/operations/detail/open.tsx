import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import { projectURL } from '../../session/project'

const OperationPanel = lazy(() => import('./OperationPanel'))

export function showOperation(id: string) {
  const base = projectURL()
  const operation = encodeURIComponent(id)
  const target = base + '/operations/' + operation
  openDetail('操作详情', (stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载…</p>}>
      <OperationPanel target={target} ticketTarget={base + '/governance/change-tickets/' + operation}
        stamp={stamp} signal={signal} reload={() => showOperation(id)} />
    </Suspense>
  ))
}
