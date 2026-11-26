import { actions, reasons, statuses } from '../../shared/display/labels'
import { showOperation } from '../operations/detail/open'
import type { BatchPreview, BatchReceipt } from './schema'

export function BatchPreviewReport({ preview }: { preview: BatchPreview }) {
  return <>
    <h2>预览结果</h2>
    <p className="hint">剩余活动操作配额 {preview.remaining}</p>
    <ol>
      {preview.items.map((item) => {
        const revisions = [
          item.ownership_revision && '实例版本 ' + item.ownership_revision,
          item.volume_revision && '卷版本 ' + item.volume_revision,
        ].filter(Boolean).join(' / ')
        return <li key={item.index}>
          {reasons[item.reason as keyof typeof reasons] || item.reason}
          {item.approval_required ? '，需要审批' : ''}
          {revisions ? <span className="minor">{revisions}</span> : null}
        </li>
      })}
    </ol>
  </>
}

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
