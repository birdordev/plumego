import { useEffect, useRef, useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { readTagPolicy, saveTagPolicy } from './api'
import type { TagPolicy, TagPolicyInput } from './schema'

type Result = { state: 'loading' | 'missing' } | { state: 'error'; message: string } | { state: 'ready'; policy: TagPolicy }

export function useTagPolicy(base: string) {
  const [version, setVersion] = useState(0)
  const [result, setResult] = useState<Result>({ state: 'loading' })
  const write = useRef<AbortController>(null)

  useEffect(() => {
    const controller = new AbortController()
    const { signal } = controller
    void readTagPolicy(base, signal).then((policy) => {
      if (!signal.aborted) setResult({ state: 'ready', policy })
    }).catch((error: unknown) => {
      if (signal.aborted) return
      if (error instanceof ConsoleRequestError && error.status === 404) setResult({ state: 'missing' })
      else setResult({ state: 'error', message: requestErrorMessage(error) })
    })
    return () => {
      controller.abort()
      write.current?.abort()
      write.current = null
    }
  }, [base, version])

  function reload() {
    setResult({ state: 'loading' })
    setVersion((value) => value + 1)
  }

  async function save(input: TagPolicyInput) {
    if (write.current || (result.state !== 'missing' && result.state !== 'ready')) return
    const controller = new AbortController()
    write.current = controller
    try {
      const revision = result.state === 'ready' ? result.policy.revision : undefined
      const policy = await saveTagPolicy(base, input, revision, controller.signal)
      if (!controller.signal.aborted) setResult({ state: 'ready', policy })
    } finally {
      if (write.current === controller) write.current = null
    }
  }

  return { result, reload, save }
}
