import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { timestamp } from '../../../shared/display/time'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { listColdMigrations, readEligibility } from './api'
import type { Context } from './open'
import { statuses, type Eligibility } from './schema'

type Props = {
  context: Context; signal: AbortSignal; refresh: () => void; select: (id: string) => void
  start?: (source: Exclude<Eligibility, { mode: 'unavailable' }>) => void; pending?: () => void; draftError?: string
}

export default function MigrationHistory({ context, signal, refresh, select, start, pending, draftError }: Props) {
  const result = useResourceRead(context.target, readEligibility, signal)
  const source = result.state === 'ready' ? result.data : undefined
  return <>
    <div className="row-actions">
      <button type="button" onClick={context.backToVM}>返回虚拟机</button>
      <button type="button" onClick={refresh}>刷新迁移记录</button>
      {pending && <button type="button" className="primary" onClick={pending}>恢复原迁移申请</button>}
    </div>
    {draftError && <p role="alert">{draftError}</p>}
    {result.state === 'error' && <p role="alert">{requestErrorMessage(result.error)}</p>}
    <PagedResults target={context.target} signal={signal} read={listColdMigrations}>{(items, first) => <>
      {first && start && source && source.mode !== 'unavailable' && !items.some((item) => item.status === 'pending') &&
        <p><button type="button" className="primary" onClick={() => start(source)}>
          {source.mode === 'cold' ? '申请冷迁移' : '申请单机疏散'}
        </button></p>}
      {start && source?.mode === 'unavailable' && <p>当前源状态尚不满足冷迁移或单机疏散条件。</p>}
      {items.length === 0 ? <p role="status">暂无迁移记录。</p> : <ConsoleTable
        columns={['迁移 ID', '类型', '源宿主机', '目标宿主机', '状态', '申请时间']}
        rows={items.map((item) => ({ key: item.migration_id, cells: [
          <button type="button" onClick={() => select(item.migration_id)}>{item.migration_id}</button>,
          item.source_fence_generation ? '故障疏散' : '冷迁移', item.source_host, item.target_host,
          statuses[item.status], timestamp(item.requested_at),
        ] }))} />}
    </>}</PagedResults>
  </>
}
