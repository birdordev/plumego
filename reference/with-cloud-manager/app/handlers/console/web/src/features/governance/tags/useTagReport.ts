import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { readTagReport } from './api'
import type { TagReport } from './schema'

export function useTagReport(base: string, revision: number) {
  const active = useRef<AbortController>(null)
  const filters = useRef('')
  const [report, setReport] = useState<TagReport>()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => () => active.current?.abort(), [])

  async function load(query: URLSearchParams, append: boolean) {
    active.current?.abort()
    const controller = new AbortController()
    const { signal } = controller
    active.current = controller
    setLoading(true)
    setError('')
    if (!append) setReport(undefined)
    try {
      const page = await readTagReport(base, query, signal)
      if (signal.aborted) return
      if (page.policy_revision !== revision) {
        setReport(undefined)
        setError('标签规则已更新，请重新读取规则后查询。')
        return
      }
      setReport((previous) => append && previous ? { ...page, items: [...previous.items, ...page.items] } : page)
    } catch (error) {
      if (!signal.aborted) setError(requestErrorMessage(error))
    } finally {
      if (active.current === controller) active.current = null
      if (!signal.aborted) setLoading(false)
    }
  }

  function search(query: URLSearchParams) {
    filters.current = query.toString()
    void load(query, false)
  }

  function more() {
    if (!report?.next || active.current) return
    const query = new URLSearchParams(filters.current)
    query.set('cursor', report.next)
    void load(query, true)
  }

  return { report, loading, error, search, more }
}
