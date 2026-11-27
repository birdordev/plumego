import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { readHost } from '../api'
import { hostStatus } from '../labels'
import { capacityFields } from './capacity'

export default function HostPanel({ target, signal, reload, onFence, onCleanups }: {
  target: string; signal: AbortSignal; reload: () => void; onFence: () => void; onCleanups: () => void
}) {
  const result = useResourceRead(target, readHost, signal)
  if (result.state === 'loading') return <p role="status">正在加载宿主机…</p>
  if (result.state === 'error') return <>
    <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={reload}>重试</button>
  </>
  const host = result.data
  return <>
    <DetailFields fields={[
      ['宿主机', host.id], ['站点', host.site], ['UUID', host.uuid], ['状态', hostStatus(host)],
      ['故障域', host.failure_domain], ['最近上报', timestamp(host.reported_at)],
      ['代理主体', host.agent], ['代理代次', host.generation], ['版本', host.revision],
      ['网络', host.networks.join('、')], ['本地存储池', host.storage_pool], ['共享存储池', host.shared_storage_pool_id],
      ...capacityFields(host.capacity),
    ]} />
    <div className="row-actions">
      <button type="button" onClick={onFence}>宿主机隔离与重新接入</button>
      <button type="button" onClick={onCleanups}>查看保留卷清理任务</button>
      <button type="button" onClick={reload}>刷新详情</button>
    </div>
  </>
}
