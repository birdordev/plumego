import { useCallback } from 'react'
import { sessionSnapshot, type Session } from '../../features/session/store'
import { canOpenView, type View } from '../routes/registry'
import { useWorkspaceNavigation } from './useWorkspaceNavigation'
import { useWorkspaceSession } from './useWorkspaceSession'

export function useConsoleWorkspace() {
  const { view, showView, closeCollection } = useWorkspaceNavigation()
  const openProject = useCallback((scope: Session, onUnauthorized: () => void) => {
    showView('assets', scope, onUnauthorized)
  }, [showView])
  const { expireSession, ...sessionState } = useWorkspaceSession({ openProject, closeCollection })

  function selectView(next: View) {
    const scope = sessionSnapshot()
    if (scope && canOpenView(next, scope)) showView(next, scope, expireSession)
  }

  return { view, ...sessionState, selectView }
}
