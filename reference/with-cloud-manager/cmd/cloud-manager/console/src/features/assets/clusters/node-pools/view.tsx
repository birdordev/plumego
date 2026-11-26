import { lazy, Suspense } from 'react'
import { openDetail } from '../../../../shared/detail/store'
import { projectURL } from '../../../session/project'
import type { ClusterAsset } from '../snapshot'

const NodePoolsPanel = lazy(() => import('./NodePoolsPanel'))

export function showNodePools(cluster: ClusterAsset) {
  const base = projectURL()
  openDetail('Kubernetes 节点池', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载节点池…</p>}>
      <NodePoolsPanel base={base} signal={signal} cluster={cluster} />
    </Suspense>
  ))
}
