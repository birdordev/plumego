import { useCallback, useEffect, useRef, useState } from 'react'
import { projectPath } from '../../features/session/project'
import { sessionSnapshot, type Session } from '../../features/session/store'
import { setWorkspaceNotice } from '../../shared/feedback/notice'
import { collectionLoaderFor, type View } from '../routes'

export function useWorkspaceNavigation() {
  const [view, setView] = useState<View>('assets')
  const active = useRef<AbortController>(undefined)

  const closeCollection = useCallback(() => {
    active.current?.abort()
    active.current = undefined
  }, [])

  useEffect(() => closeCollection, [closeCollection])

  const showView = useCallback((next: View, scope: Session, onUnauthorized: () => void) => {
    closeCollection()
    setView(next)
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
        onUnauthorized() {
          if (sessionSnapshot() === scope) onUnauthorized()
        },
      })
    }).catch(() => {
      if (!signal.aborted && sessionSnapshot() === scope) {
        setWorkspaceNotice('无法加载列表，请刷新后重试。', true)
      }
    })
  }, [closeCollection])

  return { view, closeCollection, showView }
}
