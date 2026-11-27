import { Suspense } from 'react'
import type { Session } from '../../features/session/store'
import type { WorkspaceLink } from '../../features/overview/navigation'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'
import { routes, type PageView } from '../routes/registry'

type Props = {
  view: PageView
  session: Session
  filters: string
  commitFilters: (filters: string) => string
  openSelectedBatch: () => void
  link: WorkspaceLink
  expireSession: () => void
}

export function WorkspacePage({ view, ...context }: Props) {
  return <FeatureBoundary key={view} message="无法加载页面，请刷新后重试。">
    <Suspense fallback={<p role="status">正在加载页面…</p>}>
      {routes[view].render(context)}
    </Suspense>
  </FeatureBoundary>
}
