import type { ReactNode } from 'react'

type Props = {
  generation: number
  items: readonly unknown[]
  loading: boolean
  next?: string
  onLoadNext: () => Promise<void>
  tools?: ReactNode
  filters?: ReactNode
  footer?: ReactNode
  children: ReactNode
}

export function CollectionPage({ generation, items, loading, next, onLoadNext, tools, filters, footer, children }: Props) {
  return (
    <section key={generation} id="collection" className="collection-page" aria-labelledby="title" aria-busy={loading}>
      {(tools || filters) && <div className="collection-toolbar">{tools}{filters}</div>}
      <div className="collection-results">
        {loading && items.length === 0 ? <div className="table-placeholder" aria-hidden="true" /> : children}
        {items.length > 0 || footer || next ? <footer className="table-actions">
          {items.length > 0 && <span className="collection-count">已加载 {items.length} 条</span>}
          {footer}
          {next ? <button id="more" onClick={() => void onLoadNext()} type="button" disabled={loading}>加载下一页</button> : null}
        </footer> : null}
      </div>
    </section>
  )
}
