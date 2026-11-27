import { useState } from 'react'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import type { VMDetail } from '../schema'
import { listTargets, requestMigration } from './api'
import { PagedResults } from '../../../shared/collection/PagedResults'
import type { Target } from './schema'

type Props = {
  target: string; vm: VMDetail; stamp: number; signal: AbortSignal
  back: () => void; refresh: () => void; onSaved: (id: string) => void
}

export default function MigrationRequest({ target, vm, stamp, signal, back, refresh, onSaved }: Props) {
  const [selected, setSelected] = useState<{ host: Target; id: string }>()
  const { busy, run } = useDetailAction(stamp)
  if (selected) {
    return <>
      <DetailFields fields={[
        ['虚拟机', vm.id], ['原宿主机', vm.host], ['目标宿主机', selected.host.id], ['共享存储池', vm.shared_storage_pool_id],
      ]} />
      <p>审批时会复核目标可用容量。宿主机代理会检查 TLS、桥接网络和目标域状态。</p>
      <div className="row-actions">
        <button type="button" className="primary" disabled={busy} onClick={() => void run(
          () => requestMigration(target, selected.id, selected.host.id, vm.revision, signal),
          (receipt) => onSaved(receipt.migration_id),
        )}>确认申请实时迁移</button>
        <button type="button" autoFocus disabled={busy} onClick={() => { setSelected(undefined); setDetailNotice(stamp) }}>重新选择目标</button>
        <button type="button" disabled={busy} onClick={back}>返回迁移记录</button>
      </div>
    </>
  }
  return <>
    <p>选择同站点、同网络和共享存储池的宿主机。</p>
    <div className="row-actions">
      <button type="button" onClick={back}>返回迁移记录</button>
      <button type="button" onClick={refresh}>刷新候选宿主机</button>
    </div>
    <PagedResults target={target} signal={signal} read={listTargets}>{(items) => items.length === 0
      ? <p role="status">当前没有可选宿主机。</p>
      : <ConsoleTable columns={['候选宿主机', '故障域']} rows={items.map((host) => ({
        key: host.id,
        cells: [<button type="button" onClick={() => setSelected({ host, id: crypto.randomUUID() })}>{host.id}</button>, host.failure_domain],
      }))} />}</PagedResults>
  </>
}
