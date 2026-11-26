import type { ReactNode } from 'react'
import { timestamp } from '../../../shared/display/labels'
import type { ClusterAsset, ClusterSnapshotReader } from './snapshot'
import { useClusterSnapshot } from './useClusterSnapshot'

type Props<T> = {
  base: string
  signal: AbortSignal
  cluster: ClusterAsset
  label: string
  unit: string
  read: ClusterSnapshotReader<T>
  children: (items: T[], snapshot: string, usable: boolean) => ReactNode
}

export function ClusterSnapshotPanel<T>({ base, cluster, label, unit, read, signal, children }: Props<T>) {
  const { page, busy, changed, error, reload, more } = useClusterSnapshot(base, cluster, read, label, signal)
  return (
    <section aria-busy={busy}>
      {page ? <p>{cluster.name || cluster.id} · {page.total} {unit}{label} · 采集时间 {timestamp(page.observed_at)}</p> : null}
      {busy ? <p role="status">正在读取{label}…</p> : null}
      {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
      {changed ? <p role="status">{label}快照已更新，请从第一页重新加载。</p> : null}
      {page?.total === 0 ? <p>当前快照没有{label}。</p> : null}
      {page?.items.length ? children(page.items, page.snapshot, !changed && !error && !busy) : null}
      <div className="table-actions">
        {changed || error ? <button type="button" disabled={busy} onClick={reload}>重新加载快照</button> : null}
        {!changed && page?.next_offset ? <button type="button" disabled={busy} onClick={more}>加载更多{label}</button> : null}
      </div>
    </section>
  )
}
