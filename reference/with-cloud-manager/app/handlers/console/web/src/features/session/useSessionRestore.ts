import { useEffect, useState } from 'react'
import { ConsoleRequestError } from '../../shared/api/requests'
import { readSession } from './api'
import type { Profile } from './store'

export function useSessionRestore(onRestored: (profile: Profile) => void, onFailed: (message: string) => void) {
  const [restoring, setRestoring] = useState(true)

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller

    void readSession(signal).then((profile) => {
      if (!signal.aborted) onRestored(profile)
    }).catch((error: unknown) => {
      if (signal.aborted || (error instanceof ConsoleRequestError && error.status === 401)) return
      onFailed(error instanceof Error ? error.message : '读取会话失败。')
    }).finally(() => {
      if (!signal.aborted) setRestoring(false)
    })

    return () => controller.abort()
  }, [onRestored, onFailed])

  return restoring
}
