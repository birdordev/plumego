import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { listCleanups } from './api'
import { isActive, statuses, type Cleanup, type RetainedVolume } from './schema'

type Props = {
  target: string; signal: AbortSignal; volume?: RetainedVolume; back: () => void; backLabel: string
  refresh: () => void; select: (item: Cleanup) => void; start?: () => void
}

export default function CleanupList({ target, signal, volume, back, backLabel, refresh, select, start }: Props) {
  return <>
    {volume && <DetailFields fields={[
      ['放置 ID', volume.placement_id], ['原宿主机', volume.host], ['启动卷', volume.boot_volume],
      ['存储池', volume.storage_pool], ['共享池', volume.shared_storage_pool_id],
    ]} />}
    <div className="row-actions">
      <button type="button" onClick={back}>{backLabel}</button>
      <button type="button" onClick={refresh}>刷新清理记录</button>
    </div>
    <PagedResults target={target} signal={signal} read={listCleanups}>{(items, first) => <>
      {first && start && !items.some((job) => job.status === 'completed' || isActive(job.status)) &&
        <p><button type="button" className="primary" onClick={start}>申请清理</button></p>}
      {items.length === 0 ? <p role="status">{volume ? '暂无清理记录。' : '暂无进行中的保留卷清理。'}</p>
        : <ConsoleTable columns={['清理 ID', '放置 ID', '状态', '申请时间']} rows={items.map((job) => ({
          key: job.placement_id + '/' + job.cleanup_id,
          cells: [<button type="button" onClick={() => select(job)}>{job.cleanup_id}</button>,
            job.placement_id, statuses[job.status], timestamp(job.requested_at)],
        }))} />}
    </>}</PagedResults>
  </>
}
