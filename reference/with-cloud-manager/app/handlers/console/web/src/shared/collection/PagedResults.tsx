import { useCallback, useState, type ReactNode } from 'react'
import { requestErrorMessage } from '../api/requests'
import { useResourceRead } from '../api/useResourceRead'

type Props<Item> = {
  target: string; signal?: AbortSignal
  read: (target: string, cursor: string, signal: AbortSignal) => Promise<{ items: Item[]; next?: string }>
  children: (items: Item[], first: boolean) => ReactNode
}

export function PagedResults<Item>(props: Props<Item>) {
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  return <ResultsPage key={cursor} {...props} cursor={cursor}
    previous={() => setCursors((items) => items.slice(0, -1))}
    advance={(next) => setCursors((items) => [...items, next])} />
}

function ResultsPage<Item>({ target, signal, read, children, cursor, previous, advance }: Props<Item> & {
  cursor: string; previous: () => void; advance: (next: string) => void
}) {
  const readPage = useCallback((path: string, requestSignal: AbortSignal) => read(path, cursor, requestSignal), [cursor, read])
  const result = useResourceRead(target, readPage, signal)
  const page = result.state === 'ready' ? result.data : undefined
  const next = page?.next
  return <>
    {result.state === 'loading' && <p role="status">正在加载…</p>}
    {result.state === 'error' && <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    {page && children(page.items, cursor === '')}
    {(cursor || next) ? <div className="row-actions">
      {cursor && <button type="button" onClick={previous}>上一页</button>}
      {next && <button type="button" onClick={() => advance(next)}>下一页</button>}
    </div> : null}
  </>
}
