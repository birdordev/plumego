import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { statuses } from '../../../shared/display/status-labels'
import { timestamp } from '../../../shared/display/time'
import { request } from '../../session/client'
import { operationEventsSchema, type OperationEvents } from './schema'

export function OperationTimeline({ initial, target, signal }: { initial: OperationEvents; target: string; signal: AbortSignal }) {
  const [pages, setPages] = useState([initial])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const last = pages.at(-1)

  async function loadMore() {
    if (!last?.has_more || loading || signal.aborted) return
    setLoading(true)
    setError('')
    try {
      const page = await request(target + '/events?after=' + encodeURIComponent(last.next_after), { signal }, operationEventsSchema)
      if (!signal.aborted) setPages((previous) => [...previous, page])
    } catch (failure) {
      if (!signal.aborted) setError(requestErrorMessage(failure))
    } finally {
      if (!signal.aborted) setLoading(false)
    }
  }

  return <>
    <h2>状态记录</h2>
    <ol>{pages.flatMap((page) => page.events.map((event, index) =>
      <li key={page.next_after + '/' + index}>{timestamp(event.recorded_at)} {statuses[event.status] || event.status}</li>))}</ol>
    {last?.has_more ? <button type="button" disabled={loading} onClick={() => void loadMore()}>加载更多事件</button> : null}
    {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
  </>
}
