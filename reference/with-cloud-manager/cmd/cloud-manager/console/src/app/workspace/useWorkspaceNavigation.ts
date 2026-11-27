import { useCallback, useEffect, useRef, useState } from 'react'
import { projectPath } from '../../features/session/project'
import { sessionSnapshot, type Session } from '../../features/session/store'
import type { CollectionLifecycle } from '../../shared/collection/store'
import { closeDetail } from '../../shared/detail/store'
import { setWorkspaceNotice } from '../../shared/feedback/notice'
import { collectionLoaderFor, type View } from '../routes/registry'
import { defaultView, updateWorkspaceLocation } from './workspace-location'
import { filterQuery } from './workspace-filters'

export function useWorkspaceNavigation() {
  const [location, setLocation] = useState({ view: defaultView, filters: '' })
  const current = useRef<{ view: View; scope: Session; filters: string }>(undefined)
  const active = useRef<AbortController>(undefined)
  const [collection, setCollection] = useState<{ controller: AbortController; lifecycle: CollectionLifecycle }>()
  const [pendingRefresh, setPendingRefresh] = useState<AbortController>()

  const closeCollection = useCallback(() => {
    active.current?.abort()
    active.current = undefined
    current.current = undefined
  }, [])

  useEffect(() => closeCollection, [closeCollection])

  const refreshCollection = useCallback(() => {
    const controller = active.current
    if (!controller || collection?.controller !== controller || pendingRefresh === controller) return
    setPendingRefresh(controller)
    void collection.lifecycle.refresh().finally(() => {
      setPendingRefresh((pending) => pending === controller ? undefined : pending)
    })
  }, [collection, pendingRefresh])

  const commitFilters = useCallback((input: string) => {
    const target = current.current
    if (!target || sessionSnapshot() !== target.scope) return ''
    const filters = filterQuery(target.view, new URLSearchParams(input))
    if (filters !== target.filters) {
      target.filters = filters
      updateWorkspaceLocation(target.scope.project.id, target.view, filters, 'pushState')
      setLocation({ view: target.view, filters })
    }
    return filters
  }, [])

  const showView = useCallback((next: View, scope: Session, filters: string, onUnauthorized: () => void) => {
    if (current.current?.view === next && current.current.scope === scope && current.current.filters === filters) return
    closeCollection()
    setCollection(undefined)
    setPendingRefresh(undefined)
    closeDetail()
    current.current = { view: next, scope, filters }
    setLocation({ view: next, filters })
    setWorkspaceNotice()
    const loadCollection = collectionLoaderFor(next)
    if (!loadCollection) return

    const controller = new AbortController()
    const { signal } = controller
    active.current = controller
    setWorkspaceNotice('正在加载…')
    void loadCollection().then((collection) => {
      if (signal.aborted || sessionSnapshot() !== scope) return
      signal.addEventListener('abort', collection.close, { once: true })
      collection.open({
        base: projectPath(scope),
        admin: scope.project.role === 'admin',
        commitFilters: (input) => signal.aborted ? input : commitFilters(input),
        onUnauthorized() {
          if (sessionSnapshot() === scope) onUnauthorized()
        },
      }, filters)
      setCollection({ controller, lifecycle: collection })
    }).catch(() => {
      if (!signal.aborted && sessionSnapshot() === scope) {
        setWorkspaceNotice('无法加载列表，请刷新后重试。', true)
      }
    })
  }, [closeCollection, commitFilters])

  return { ...location, closeCollection, commitFilters, showView,
    refreshCollection: collection ? refreshCollection : undefined, refreshing: pendingRefresh !== undefined }
}
