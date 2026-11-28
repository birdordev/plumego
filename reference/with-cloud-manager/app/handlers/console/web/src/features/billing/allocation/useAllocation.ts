import { useEffect, useRef, useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { readAllocation } from './api'
import type { Allocation, AllocationQuery } from './schema'

type Result = { state: 'idle' | 'loading' } | { state: 'ready'; value: Allocation } | { state: 'error'; message: string }

export function useAllocation(base: string) {
  const [result, setResult] = useState<Result>({ state: 'idle' })
  const active = useRef<AbortController>(undefined)
  useEffect(() => () => active.current?.abort(), [base])

  function rejectQuery() {
    active.current?.abort()
    setResult({ state: 'error', message: '请核对账号、账期和版本。指定分摊版本时同时填写账单版本。' })
  }

  async function load(key: AllocationQuery) {
    active.current?.abort()
    const controller = new AbortController()
    active.current = controller
    const { signal } = controller
    setResult({ state: 'loading' })
    try {
      const value = await readAllocation(base, key, signal)
      if (!signal.aborted) setResult({ state: 'ready', value })
    } catch (error) {
      if (!signal.aborted) setResult({
        state: 'error', message: error instanceof ConsoleRequestError && error.status === 404
          ? '该账单版本尚无对应的分摊结果。' : requestErrorMessage(error),
      })
    } finally {
      if (active.current === controller) active.current = undefined
    }
  }

  return { result, load, rejectQuery }
}
