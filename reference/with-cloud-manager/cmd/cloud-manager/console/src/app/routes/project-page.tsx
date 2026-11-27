import { lazy, type ComponentType } from 'react'
import type { Session } from '../../features/session/store'
import type { CollectionLifecycle } from '../../shared/collection/store'
import type { WorkspaceLink } from '../../features/overview/navigation'

type PageContext = {
  session: Session
  filters: string
  commitFilters: (filters: string) => string
  openSelectedBatch: () => void
  link: WorkspaceLink
  expireSession: () => void
}

type PageOptions = {
  allowed?: (session: Session) => boolean
  loadCollection?: () => Promise<CollectionLifecycle>
}

export function projectPage<Props extends object>(
  title: string,
  load: () => Promise<{ default: ComponentType<Props> }>,
  props: (context: PageContext) => Props,
  options: PageOptions = {},
) {
  const Page = lazy(load)
  return {
    title,
    ...options,
    render(context: PageContext) {
      return <Page {...props(context)} />
    },
  }
}
