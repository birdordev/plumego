import { Suspense } from 'react'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'
import { routes, type PageProps, type PageView } from '../routes'

type Props = PageProps & { view: PageView }

export function WorkspacePage({ view, ...props }: Props) {
  const Page = routes[view].Page
  return <FeatureBoundary key={view} message="无法加载页面，请刷新后重试。">
    <Suspense fallback={<p role="status">正在加载页面…</p>}>
      <Page {...props} />
    </Suspense>
  </FeatureBoundary>
}
