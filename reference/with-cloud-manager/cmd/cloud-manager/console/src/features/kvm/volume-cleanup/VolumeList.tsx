import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { PagedResults } from '../detail/PagedResults'
import { listVolumes } from './api'
import { statuses, type RetainedVolume } from './schema'

type Props = {
  target: string; signal: AbortSignal; back: () => void; refresh: () => void; select: (volume: RetainedVolume) => void
}

export default function VolumeList({ target, signal, back, refresh, select }: Props) {
  return <>
    <div className="row-actions">
      <button type="button" onClick={back}>返回虚拟机</button>
      <button type="button" onClick={refresh}>刷新保留卷</button>
    </div>
    <PagedResults target={target} signal={signal} read={listVolumes}>{(items) => items.length === 0
      ? <p role="status">暂无保留卷或清理记录。</p>
      : <ConsoleTable columns={['放置 ID', '原宿主机', '启动卷', '状态']} rows={items.map((volume) => ({
        key: volume.placement_id,
        cells: [<button type="button" onClick={() => select(volume)}>{volume.placement_id}</button>,
          volume.host, volume.boot_volume, volume.latest_status ? statuses[volume.latest_status] : '保留中'],
      }))} />}</PagedResults>
  </>
}
