import type { ReactNode } from 'react'

type Props = {
  generation: number
  loading: boolean
  next?: string
  onLoadNext: () => Promise<void>
  tools?: ReactNode
  filters?: ReactNode
  footer?: ReactNode
  children: ReactNode
}

export function CollectionPage({ generation, loading, next, onLoadNext, tools, filters, footer, children }: Props) {
  return (
    <section key={generation} id="collection" aria-labelledby="title" aria-busy={loading}>
      {tools}
      {filters}
      {children}
      <footer className="table-actions">
        {footer}
        {next ? <button id="more" onClick={() => void onLoadNext()} type="button" disabled={loading}>加载下一页</button> : null}
      </footer>
    </section>
  )
}
