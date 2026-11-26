import { Suspense } from 'react'
import type { Session } from '../../features/session/store'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'
import { routes, type PageView } from '../routes/registry'

type Props = { view: PageView; session: Session; openSelectedBatch: () => void }

export function WorkspacePage({ view, session, openSelectedBatch }: Props) {
  return <FeatureBoundary key={view} message="无法加载页面，请刷新后重试。">
    <Suspense fallback={<p role="status">正在加载页面…</p>}>
      {routes[view].render({ session, openSelectedBatch })}
    </Suspense>
  </FeatureBoundary>
}
