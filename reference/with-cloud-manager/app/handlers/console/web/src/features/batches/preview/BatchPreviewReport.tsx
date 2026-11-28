import { reasons } from './labels'
import type { BatchPreview } from './schema'

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
          {item.ticket_required ? '，需要外部工单和独立审批' : item.approval_required ? '，需要审批' : ''}
          {revisions ? <span className="minor">{revisions}</span> : null}
        </li>
      })}
    </ol>
  </>
}
