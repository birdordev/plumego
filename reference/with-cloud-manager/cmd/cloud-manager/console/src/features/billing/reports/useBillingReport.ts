import { useEffect, useRef, useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { readBillingReport } from './api'
import type { BillingQuery } from '../schema'
import type { BillingReport } from './schema'

type Result = { state: 'idle' | 'loading' } | { state: 'ready'; report: BillingReport } | { state: 'error'; message: string }

export function useBillingReport(base: string) {
  const [result, setResult] = useState<Result>({ state: 'idle' })
  const active = useRef<AbortController>(undefined)

  useEffect(() => () => active.current?.abort(), [base])

  async function load(query: BillingQuery) {
    active.current?.abort()
    const controller = new AbortController()
    active.current = controller
    const { signal } = controller
    setResult({ state: 'loading' })
    try {
      const report = await readBillingReport(base, query, signal)
      if (!signal.aborted) setResult({ state: 'ready', report })
    } catch (error) {
      if (!signal.aborted) setResult({ state: 'error', message: error instanceof ConsoleRequestError && error.status === 404
        ? '当前条件下没有已发布的账单版本。' : requestErrorMessage(error) })
    } finally {
      if (active.current === controller) active.current = undefined
    }
  }

  function invalidQuery() {
    active.current?.abort()
    setResult({ state: 'error', message: '请核对账号、账期和版本。' })
  }

  return { result, load, invalidQuery }
}
