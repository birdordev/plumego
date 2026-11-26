import { useEffect, useRef, useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { readBudget } from './api'
import type { Budget, BudgetKey } from './schema'

type Result =
  | { state: 'idle' | 'loading' }
  | { state: 'ready'; key: BudgetKey; budget?: Budget }
  | { state: 'error'; message: string }

export function useBudgetLookup(base: string) {
  const [result, setResult] = useState<Result>({ state: 'idle' })
  const active = useRef<AbortController>(undefined)

  useEffect(() => () => active.current?.abort(), [base])

  function showError(message: string) {
    active.current?.abort()
    setResult({ state: 'error', message })
  }

  async function load(key: BudgetKey) {
    active.current?.abort()
    const controller = new AbortController()
    active.current = controller
    const { signal } = controller
    setResult({ state: 'loading' })
    try {
      const budget = await readBudget(base, key, signal)
      if (!signal.aborted) setResult({ state: 'ready', key, budget })
    } catch (error) {
      if (signal.aborted) return
      if (error instanceof ConsoleRequestError && error.status === 404) setResult({ state: 'ready', key })
      else showError(requestErrorMessage(error))
    } finally {
      if (active.current === controller) active.current = undefined
    }
  }

  function apply(key: BudgetKey, budget: Budget) {
    setResult((current) => current.state === 'ready' && current.key === key ? { state: 'ready', key, budget } : current)
  }

  return { result, load, showError, apply }
}
