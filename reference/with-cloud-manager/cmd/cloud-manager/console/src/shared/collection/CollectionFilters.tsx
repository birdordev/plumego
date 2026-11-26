import type { FormEvent, ReactNode } from 'react'
import { setWorkspaceNotice } from '../feedback/notice'

type Props = {
  children: ReactNode
  onApply: (filters: string) => Promise<void>
  dateFields?: readonly string[]
}

export function CollectionFilters({ children, onApply, dateFields }: Props) {
  function submitFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const query = new URLSearchParams()
    for (const [field, value] of new FormData(event.currentTarget)) {
      if (typeof value !== 'string' || value === '') continue
      if (dateFields?.includes(field)) {
        const date = new Date(value)
        if (!Number.isFinite(date.valueOf())) {
          setWorkspaceNotice('请填写有效的时间。', true)
          return
        }
        query.set(field, date.toISOString())
      } else {
        query.set(field, value)
      }
    }
    void onApply(query.toString())
  }

  return <form id="filters" onSubmit={submitFilters} className="filters">
    {children}
    <button className="primary">筛选</button>
  </form>
}
