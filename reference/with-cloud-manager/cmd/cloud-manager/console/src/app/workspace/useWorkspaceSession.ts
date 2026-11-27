import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import { clearSelection } from '../../features/assets/instances/selection-store'
import { clearCatalogAttempts } from '../../features/catalog/launch/attempts'
import { clearOperationAttempts } from '../../features/operations/forms/attempts'
import { endSession, readSession, startSession } from '../../features/session/api'
import { sessionSnapshot, setSession, subscribeSession, type Profile, type Session } from '../../features/session/store'
import { useSessionRestore } from '../../features/session/useSessionRestore'
import { closeDetail } from '../../shared/detail/store'
import { setWorkspaceNotice } from '../../shared/feedback/notice'
import { canOpenView, type View } from '../routes/registry'
import { clearWorkspaceLocation, defaultView, readWorkspaceLocation, type HistoryAction } from './workspace-location'

type Notice = { text: string; error?: boolean }

type Navigation = {
  openLocation: (session: Session, view: View, filters: string, action: HistoryAction, onUnauthorized: () => void) => void
  closeCollection: () => void
}

export function useWorkspaceSession({ openLocation, closeCollection }: Navigation) {
  const session = useSyncExternalStore(subscribeSession, sessionSnapshot)
  const [notice, setNotice] = useState<Notice>()
  const [ending, setEnding] = useState(false)

  const clearWorkspace = useCallback(() => {
    closeCollection()
    setSession(null)
    closeDetail()
    clearSelection()
    clearCatalogAttempts()
    clearOperationAttempts()
    setWorkspaceNotice()
  }, [closeCollection])

  const expireSession = useCallback(() => {
    clearWorkspace()
    setNotice({ text: '会话已失效。请重新登录。', error: true })
  }, [clearWorkspace])

  const enterProject = useCallback((scope: Session, view: View, filters: string, action: HistoryAction) => {
    const current = sessionSnapshot()
    if (current?.profile === scope.profile && current.project === scope.project) {
      openLocation(current, view, filters, action, expireSession)
      return
    }
    closeDetail()
    clearSelection()
    setSession(scope)
    openLocation(scope, view, filters, action, expireSession)
  }, [openLocation, expireSession])

  const enterProfile = useCallback((profile: Profile) => {
    const target = readWorkspaceLocation(profile)
    if (!target) {
      clearWorkspace()
      setNotice({ text: '当前身份没有可查看的项目。', error: true })
      return
    }
    setNotice(undefined)
    enterProject(target.scope, target.view, target.filters, 'replaceState')
  }, [enterProject, clearWorkspace])

  const showRestoreError = useCallback((text: string) => setNotice({ text, error: true }), [])
  const restoring = useSessionRestore(enterProfile, showRestoreError)

  useEffect(() => {
    function followHistory() {
      const scope = sessionSnapshot()
      if (scope) enterProfile(scope.profile)
    }
    window.addEventListener('popstate', followHistory)
    return () => window.removeEventListener('popstate', followHistory)
  }, [enterProfile])

  return {
    session, notice, restoring, ending, expireSession,
    selectProject(id: string, view: View) {
      const scope = sessionSnapshot()
      const project = scope?.profile.projects.find((item) => item.id === id)
      if (scope && project && project.id !== scope.project.id) {
        const next = { profile: scope.profile, project }
        enterProject(next, canOpenView(view, next) ? view : defaultView, '', 'pushState')
      }
    },
    async login(tenant: string) {
      await startSession(tenant)
      enterProfile(await readSession())
    },
    async logout() {
      if (ending) return
      setEnding(true)
      try {
        await endSession()
        clearWorkspace()
        clearWorkspaceLocation()
        setNotice({ text: '已退出本地会话。' })
      } catch (error) {
        setWorkspaceNotice(error instanceof Error ? error.message : '退出失败。', true)
      } finally {
        setEnding(false)
      }
    },
  }
}
