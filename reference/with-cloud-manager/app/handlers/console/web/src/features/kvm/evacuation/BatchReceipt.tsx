import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { statuses } from '../cold-migration/schema'
import { readBatch } from './api'

export default function BatchReceipt({ target, signal, back, backLabel, refresh, openVM }: {
  target: string; signal: AbortSignal; back: () => void; backLabel: string; refresh: () => void; openVM: (vm: string, id: string) => void
}) {
  const result = useResourceRead(target, readBatch, signal)
  const batch = result.state === 'ready' ? result.data : undefined
  return <>
    <div className="row-actions">
      <button type="button" onClick={back}>{backLabel}</button>
      <button type="button" onClick={refresh}>刷新批次回执</button>
    </div>
    {result.state === 'loading' && <p role="status">正在读取疏散批次…</p>}
    {result.state === 'error' && <p role="alert">{requestErrorMessage(result.error)}</p>}
    {batch && <>
      <DetailFields fields={[
        ['批次 ID', batch.batch_id], ['源宿主机', batch.source_host], ['宿主机版本', batch.host_revision],
        ['隔离代次', batch.source_fence_generation], ['申请人', batch.requester], ['受理时间', timestamp(batch.requested_at)],
      ]} />
      <h3>成员迁移回执</h3>
      <ol className="evacuation-list">{batch.items.map((item) => <li key={item.migration_id}>
        <DetailFields fields={[
          ['虚拟机', item.vm_id], ['迁移 ID', item.migration_id], ['审批状态', statuses[item.status]], ['目标宿主机', item.target_host],
          ['源端清理', item.status === 'approved' ? item.cleanup_at ? '已确认' : '待现场确认' : undefined],
        ]} />
        <button type="button" onClick={() => openVM(item.vm_id, item.migration_id)} aria-label={'查看迁移回执 ' + item.vm_id}>查看迁移回执</button>
      </li>)}</ol>
    </>}
  </>
}
