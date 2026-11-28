import { useCallback } from 'react'
import { sessionSnapshot, type Session } from '../../features/session/store'
import { canOpenView, type View } from '../routes/registry'
import { useWorkspaceNavigation } from './useWorkspaceNavigation'
import { useWorkspaceSession } from './useWorkspaceSession'
import { updateWorkspaceLocation, type HistoryAction } from './workspace-location'
import { filterQuery } from './workspace-filters'

export function useConsoleWorkspace() {
  const { view, filters, commitFilters, showView, closeCollection, refreshing, refreshCollection } = useWorkspaceNavigation()
  const openLocation = useCallback((scope: Session, next: View, restored: string, action: HistoryAction, onUnauthorized: () => void) => {
    updateWorkspaceLocation(scope.project.id, next, restored, action)
    showView(next, scope, restored, onUnauthorized)
  }, [showView])
  const { expireSession, selectProject, ...sessionState } = useWorkspaceSession({ openLocation, closeCollection })

  function selectView(next: View, input?: string) {
    const scope = sessionSnapshot()
    if (!scope || !canOpenView(next, scope)) return
    const nextFilters = input === undefined ? (next === view ? filters : '') : filterQuery(next, new URLSearchParams(input))
    openLocation(scope, next, nextFilters, 'pushState', expireSession)
  }

  return { view, filters, commitFilters, ...sessionState, selectView, expireSession, refreshing, refreshCollection,
    selectProject: (id: string) => selectProject(id, view) }
}
