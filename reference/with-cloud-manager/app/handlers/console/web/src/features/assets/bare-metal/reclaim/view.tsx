import { lazy, Suspense } from 'react'
import { openDetail } from '../../../../shared/detail/store'
import { projectPath } from '../../../session/project'
import { sessionSnapshot } from '../../../session/store'
import type { Asset } from '../../types'

const ReclaimPanel = lazy(() => import('./ReclaimPanel'))

export function showBareMetalReclaim(asset: Asset) {
  const session = sessionSnapshot()
  if (!session) return
  const project = projectPath(session)
  openDetail('裸金属回收与归属', (stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载回收记录…</p>}>
      <ReclaimPanel key={stamp} asset={asset} project={project} session={session} stamp={stamp} signal={signal}
        reload={() => showBareMetalReclaim(asset)} />
    </Suspense>
  ))
}
