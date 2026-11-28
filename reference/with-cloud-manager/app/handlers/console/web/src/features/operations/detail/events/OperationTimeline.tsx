import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../../../shared/api/requests'
import { statuses } from '../../../../shared/display/status-labels'
import { timestamp } from '../../../../shared/display/time'
import { readOperationEvents } from './api'
import { operationEventSchema, type OperationEvent, type OperationEvents } from './schema'

export function OperationTimeline({ initial, target, signal, reload }: {
  initial: OperationEvents; target: string; signal: AbortSignal; reload: () => void
}) {
  const [pages, setPages] = useState([initial])
  const [updates, setUpdates] = useState<OperationEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [live, setLive] = useState('')
  const source = useRef<EventSource>(null)
  const last = pages.at(-1)
  const latestStatus = (updates.at(-1) ?? last?.events.at(-1))?.status
  const finished = latestStatus === 'succeeded' || latestStatus === 'failed'

  useEffect(() => {
    function disconnect() {
      source.current?.close()
      source.current = null
    }
    signal.addEventListener('abort', disconnect)
    return () => {
      signal.removeEventListener('abort', disconnect)
      disconnect()
    }
  }, [signal, target])

  async function loadMore() {
    if (!last?.has_more || loading || signal.aborted) return
    setLoading(true)
    setError('')
    try {
      const page = await readOperationEvents(target, signal, last.next_after)
      if (!signal.aborted) setPages((previous) => [...previous, page])
    } catch (failure) {
      if (!signal.aborted) setError(requestErrorMessage(failure))
    } finally {
      if (!signal.aborted) setLoading(false)
    }
  }

  function stop() {
    source.current?.close()
    source.current = null
    setLive('')
  }

  function start() {
    if (!last || last.has_more || source.current || signal.aborted) return
    let cursor = BigInt(updates.at(-1)?.sequence ?? last.next_after)
    const url = target + '/events/stream?' + new URLSearchParams({ after: cursor.toString() })
    const stream = new EventSource(url)
    source.current = stream
    setError('')
    setLive('正在连接…')
    stream.onopen = () => { if (source.current === stream) setLive('正在跟踪新事件') }
    stream.onerror = () => {
      if (source.current !== stream) return
      if (stream.readyState === EventSource.CLOSED) {
        stop()
        setError('暂时无法跟踪。可重新开启或读取最新详情。')
      } else {
        setLive('连接中断，正在续读…')
      }
    }
    stream.addEventListener('access-revoked', () => {
      if (source.current !== stream) return
      stop()
      setError('已失去项目访问权限。')
    })
    stream.addEventListener('operation', (message: MessageEvent<string>) => {
      if (source.current !== stream) return
      let event: OperationEvent
      try {
        event = operationEventSchema.parse(JSON.parse(message.data))
        if (event.sequence !== message.lastEventId) throw new Error('mismatched event ID')
      } catch {
        stop()
        setError('无法读取操作事件。可重新读取最新详情。')
        return
      }
      const sequence = BigInt(event.sequence)
      if (sequence <= cursor) return
      cursor = sequence
      setUpdates((previous) => [...previous, event])
      if (event.status === 'succeeded' || event.status === 'failed') stop()
    })
  }

  return <>
    <h2>状态记录</h2>
    <ol>{[...pages.flatMap((page) => page.events), ...updates].map((event) =>
      <li key={event.sequence}>{timestamp(event.recorded_at)} {statuses[event.status] || event.status}</li>)}</ol>
    {last?.has_more ? <button type="button" disabled={loading} onClick={() => void loadMore()}>加载更多事件</button> : null}
    {!last?.has_more && (!finished || updates.length > 0) ? <div className="row-actions">
      {live ? <button type="button" onClick={stop}>停止跟踪</button>
        : !finished ? <button type="button" onClick={start}>实时跟踪</button> : null}
      {updates.length > 0 ? <button type="button" onClick={reload}>读取最新详情</button> : null}
      {live ? <span role="status">{live}</span> : null}
    </div> : null}
    {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
  </>
}
