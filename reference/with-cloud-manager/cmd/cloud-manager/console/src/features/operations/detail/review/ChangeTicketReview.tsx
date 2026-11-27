import type { ReactNode } from 'react'
import { requestErrorMessage } from '../../../../shared/api/requests'
import { DetailFields } from '../../../../shared/components/DetailFields'
import { useResourceRead } from '../../../../shared/api/useResourceRead'
import { timestamp } from '../../../../shared/display/time'
import { readChangeTicket } from './api'

type Props = {
  target: string
  signal: AbortSignal
  reload: () => void
  children: (ready: boolean) => ReactNode
}

export function ChangeTicketReview({ target, signal, reload, children }: Props) {
  const result = useResourceRead(target, readChangeTicket, signal)
  const ticket = result.state === 'ready' ? result.data : undefined
  const evidence = ticket?.evidence
  return <>
    <section aria-label="外部变更工单">
      <h3>外部变更工单</h3>
      {result.state === 'loading' ? <p role="status">正在读取工单…</p> : null}
      {result.state === 'error' ? <p role="alert">{requestErrorMessage(result.error)}</p> : null}
      {ticket ? <DetailFields fields={[
        ['策略版本', ticket.policy_revision],
        ['工单', evidence?.ticket_id ?? '等待外部工单回执'],
        ['外部批准', ticket.ready ? '有效' : evidence?.decision === 'revoked' ? '已撤回' : '当前无有效批准'],
        ['有效期', evidence?.valid_until ? timestamp(evidence.valid_until) : '—'],
      ]} /> : null}
      <button type="button" onClick={reload}>刷新工单状态</button>
    </section>
    {children(ticket?.ready ?? false)}
  </>
}
