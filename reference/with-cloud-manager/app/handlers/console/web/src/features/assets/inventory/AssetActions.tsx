import { RowAction } from '../../../shared/components/RowAction'
import type { AssetRowAction } from '../row-action'

export function AssetActions({ actions }: { actions: AssetRowAction[] }) {
  if (actions.length === 0) return '—'
  return <div className="row-actions">
    {actions.map(({ label, run }) => <RowAction key={label} onClick={run}>{label}</RowAction>)}
  </div>
}
