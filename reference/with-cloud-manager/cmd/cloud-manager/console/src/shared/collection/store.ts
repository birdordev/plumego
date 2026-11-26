import { ConsoleRequestError, requestErrorMessage } from '../api/requests'
import { setWorkspaceNotice } from '../feedback/notice'
import type { PageRequest } from './read-page'

export type CollectionScope = { base: string; admin: boolean; onUnauthorized: () => void }

export type CollectionLifecycle = {
  open: (scope: CollectionScope) => void
  close: () => void
}

type Page<Item> = { items: readonly Item[]; next?: string }
type PageReader<Item> = (query: PageRequest) => Promise<Page<Item>>
type Collection<Item> = Page<Item> & {
  scope: CollectionScope | null
  generation: number
  filters: string
  loading: boolean
}

export function createCollection<Item>(readPage: PageReader<Item>) {
  const empty: Collection<Item> = { scope: null, generation: 0, filters: '', items: [], loading: false }
  const listeners = new Set<() => void>()
  let current = empty
  let activeRequest: AbortController | undefined

  function publish(next: Collection<Item>) {
    current = next
    listeners.forEach((notify) => notify())
  }

  function close() {
    activeRequest?.abort()
    activeRequest = undefined
    if (current.scope) publish({ ...empty, generation: current.generation })
  }

  function open(scope: CollectionScope) {
    activeRequest?.abort()
    publish({ ...empty, scope, generation: current.generation + 1 })
    void load()
  }

  async function load(append = false, filters = current.filters) {
    const { scope, generation, next, loading } = current
    if (!scope || (append && (loading || !next))) return
    const controller = new AbortController()
    const { signal } = controller
    activeRequest?.abort()
    activeRequest = controller

    const previous = filters === current.filters ? current : empty
    const { items, next: priorCursor } = previous
    publish({ scope, generation, filters, items, next: priorCursor, loading: true })
    setWorkspaceNotice('正在加载…')
    try {
      const page = await readPage({ base: scope.base, admin: scope.admin, filters, after: append && next ? next : '', signal })
      if (signal.aborted) return
      const rows = append ? [...items, ...page.items] : page.items
      publish({ scope, generation, filters, items: rows, next: page.next, loading: false })
      setWorkspaceNotice(rows.length ? '' : '当前筛选没有结果。')
    } catch (error) {
      if (signal.aborted) return
      publish({ scope, generation, filters, items, next: priorCursor, loading: false })
      if (error instanceof ConsoleRequestError && error.status === 401) scope.onUnauthorized()
      else setWorkspaceNotice(requestErrorMessage(error), true)
    } finally {
      if (activeRequest === controller) activeRequest = undefined
    }
  }

  return {
    open, close,
    snapshot: () => current,
    subscribe(notify: () => void) {
      listeners.add(notify)
      return () => { listeners.delete(notify) }
    },
    refresh: () => load(),
    applyFilters: (filters: string) => load(false, filters),
    loadNext: () => load(true),
  }
}
