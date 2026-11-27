import type { FormEvent, ReactNode } from 'react'
import { ConsoleIcon } from '../components/ConsoleIcon'
import { localDateTime } from '../display/time'
import { setWorkspaceNotice } from '../feedback/notice'

type Props = {
  children: (values: URLSearchParams) => ReactNode
  values: string
  onApply: (filters: string) => void | Promise<void>
  dateFields?: readonly string[]
  clearTo?: string
}

export function CollectionFilters({ children, values, onApply, dateFields, clearTo = '' }: Props) {
  function submitFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const previous = new URLSearchParams(values)
    const query = new URLSearchParams()
    for (const [field, value] of new FormData(event.currentTarget)) {
      if (typeof value !== 'string' || value === '') continue
      if (dateFields?.includes(field)) {
        const date = new Date(value)
        if (!Number.isFinite(date.valueOf())) {
          setWorkspaceNotice('请填写有效的时间。', true)
          return
        }
        const original = previous.get(field)
        // 未修改的本地时间保留原始时刻，包括夏令时回拨时两个同名时段。
        const unchanged = original && Date.parse(value + 'Z') === Date.parse(localDateTime(original) + 'Z')
        query.set(field, unchanged ? original : date.toISOString())
      } else {
        query.set(field, value)
      }
    }
    void onApply(query.toString())
  }

  return <form key={values} id="filters" onSubmit={submitFilters} className="filters" aria-label="列表筛选">
    {children(new URLSearchParams(values))}
    <div className="filter-actions">
      <button type="submit" className="primary"><ConsoleIcon name="filter" />筛选</button>
      {values !== clearTo && <button type="button" onClick={() => void onApply(clearTo)}>清除筛选</button>}
    </div>
  </form>
}
