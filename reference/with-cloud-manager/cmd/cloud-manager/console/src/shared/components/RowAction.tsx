import type { ReactNode } from 'react'
import { statuses } from '../display/status-labels'

export function RowAction({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return <button type="button" onClick={onClick}>{children}</button>
}

export function StateBadge({ status }: { status: string }) {
  return <span className="state" data-state={status}>{status in statuses ? statuses[status as keyof typeof statuses] : status || '未知'}</span>
}
