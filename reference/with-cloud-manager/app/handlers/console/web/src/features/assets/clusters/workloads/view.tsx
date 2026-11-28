import { lazy, Suspense } from 'react'
import { openDetail } from '../../../../shared/detail/store'
import { projectURL } from '../../../session/project'
import type { ClusterAsset } from '../snapshot'
import type { Asset } from '../../types'

const WorkloadsPanel = lazy(() => import('./WorkloadsPanel'))

export type WorkloadCluster = ClusterAsset & Pick<Asset, 'ownership_revision'>

export function showWorkloads(cluster: WorkloadCluster, canExecute: boolean) {
  const base = projectURL()
  openDetail('Kubernetes 工作负载', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载工作负载…</p>}>
      <WorkloadsPanel base={base} signal={signal} cluster={cluster} canExecute={canExecute} />
    </Suspense>
  ))
}
