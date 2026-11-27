import { statuses } from '../../../shared/display/status-labels'
import { showOperation } from '../../operations/detail/open'
import { actions } from '../../operations/labels'
import type { BatchReceipt } from './schema'

export function BatchReceiptReport({ receipt }: { receipt: BatchReceipt }) {
  return <>
    <h2>批次 {receipt.id}</h2>
    <ul>
      {receipt.operations.map((operation) => <li key={operation.id}>
        <button type="button" onClick={() => void showOperation(operation.id)}>
          {actions[operation.action as keyof typeof actions] || operation.action} {operation.asset_id || operation.id}
        </button>{' '}
        <span className="state" data-state={operation.status}>{statuses[operation.status as keyof typeof statuses] || operation.status}</span>
      </li>)}
    </ul>
  </>
}
