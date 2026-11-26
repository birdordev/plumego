import { useCallback, useState, useSyncExternalStore } from 'react'
import { clearSelection } from '../../features/assets/instances/selection-store'
import { clearCatalogAttempts } from '../../features/catalog/launch/attempts'
import { clearOperationAttempts } from '../../features/operations/attempts'
import { endSession, readSession, startSession } from '../../features/session/api'
import { sessionSnapshot, setSession, subscribeSession, type Profile, type Session } from '../../features/session/store'
import { useSessionRestore } from '../../features/session/useSessionRestore'
import { closeDetail } from '../../shared/detail/store'
import { setWorkspaceNotice } from '../../shared/feedback/notice'

type Notice = { text: string; error?: boolean }

type Navigation = {
  openProject: (session: Session, onUnauthorized: () => void) => void
  closeCollection: () => void
}

export function useWorkspaceSession({ openProject, closeCollection }: Navigation) {
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

  const enterProject = useCallback((scope: Session) => {
    closeDetail()
    clearSelection()
    setSession(scope)
    openProject(scope, expireSession)
  }, [openProject, expireSession])

  const enterProfile = useCallback((profile: Profile) => {
    const [project] = profile.projects
    if (!project) {
      clearWorkspace()
      setNotice({ text: '当前身份没有可查看的项目。', error: true })
      return
    }
    setNotice(undefined)
    enterProject({ profile, project })
  }, [enterProject, clearWorkspace])

  const showRestoreError = useCallback((text: string) => setNotice({ text, error: true }), [])
  const restoring = useSessionRestore(enterProfile, showRestoreError)

  return {
    session, notice, restoring, ending, expireSession,
    selectProject(id: string) {
      const scope = sessionSnapshot()
      const project = scope?.profile.projects.find((item) => item.id === id)
      if (scope && project && project.id !== scope.project.id) enterProject({ profile: scope.profile, project })
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
        setNotice({ text: '已退出本地会话。' })
      } catch (error) {
        setWorkspaceNotice(error instanceof Error ? error.message : '退出失败。', true)
      } finally {
        setEnding(false)
      }
    },
  }
}
