import type { ReactNode } from 'react'
import { statuses } from '../display/status-labels'

export function RowAction({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return <button type="button" onClick={onClick}>{children}</button>
}

export function StateBadge({ status, label }: { status: string; label?: string }) {
  const fallback = Object.hasOwn(statuses, status) ? statuses[status] : status || '未知'
  return <span className="state" data-state={status}>{label || fallback}</span>
}
