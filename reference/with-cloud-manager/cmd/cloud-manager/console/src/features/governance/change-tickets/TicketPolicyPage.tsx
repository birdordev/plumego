import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { readTicketPolicy } from './api'
import { TicketPolicyEditor } from './TicketPolicyEditor'

export default function TicketPolicyPage({ base }: { base: string }) {
  const [version, setVersion] = useState(0)
  const reload = () => setVersion((value) => value + 1)
  return <LoadTicketPolicy key={version} target={base + '/governance/ticket-policy'} reload={reload} />
}

function LoadTicketPolicy({ target, reload }: { target: string; reload: () => void }) {
  const result = useResourceRead(target, readTicketPolicy)
  if (result.state === 'loading') return <p role="status">正在读取工单策略…</p>
  if (result.state === 'error') return <>
    <p role="alert">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={reload}>重新读取策略</button>
  </>
  return <TicketPolicyEditor target={target} initial={result.data} reload={reload} />
}
