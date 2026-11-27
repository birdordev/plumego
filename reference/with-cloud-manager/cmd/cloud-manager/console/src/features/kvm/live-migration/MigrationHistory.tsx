import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { timestamp } from '../../../shared/display/time'
import { listMigrations } from './api'
import type { MigrationContext } from './open'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { canRequest, isActive, statuses } from './state'

type Props = {
  context: MigrationContext; signal: AbortSignal; refresh: () => void; start: () => void; select: (id: string) => void
}

export default function MigrationHistory({ context: { vm, target, role, backToVM }, signal, refresh, start, select }: Props) {
  return <>
    <p>虚拟机 {vm.id}</p>
    <div className="row-actions">
      <button type="button" onClick={backToVM}>返回虚拟机</button>
      <button type="button" onClick={refresh}>刷新迁移记录</button>
    </div>
    <PagedResults target={target} signal={signal} read={listMigrations}>{(items, first) => <>
      {first && canRequest(role, vm) && !items.some((item) => isActive(item.status)) &&
        <p><button type="button" className="primary" onClick={start}>申请实时迁移</button></p>}
      {items.length === 0 ? <p role="status">暂无实时迁移记录。</p> :
        <ConsoleTable columns={['迁移 ID', '源宿主机', '目标宿主机', '状态', '申请时间']} rows={items.map((item) => ({
          key: item.migration_id,
          cells: [<button type="button" onClick={() => select(item.migration_id)}>{item.migration_id}</button>,
            item.source_host, item.target_host, statuses[item.status], timestamp(item.requested_at)],
        }))} />}
    </>}</PagedResults>
  </>
}
