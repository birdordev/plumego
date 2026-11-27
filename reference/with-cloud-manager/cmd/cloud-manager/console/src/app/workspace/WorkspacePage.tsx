import { Suspense } from 'react'
import type { Session } from '../../features/session/store'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'
import type { ProjectPageLink } from '../routes/project-page'
import { routes, type PageView } from '../routes/registry'

type Props = {
  view: PageView
  session: Session
  filters: string
  commitFilters: (filters: string) => string
  openSelectedBatch: () => void
  link: ProjectPageLink
  expireSession: () => void
}

export function WorkspacePage({ view, ...context }: Props) {
  return <FeatureBoundary key={view} message="无法加载页面，请刷新后重试。">
    <Suspense fallback={<p role="status">正在加载页面…</p>}>
      {routes[view].render(context)}
    </Suspense>
  </FeatureBoundary>
}
