import { useCallback, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { vmStatus } from '../labels'
import type { HostDetail } from '../schema'
import { readFenceWork } from './api'
import { observationsFor } from './recovery/schema'
import type { WorkItem, WorkPage } from './schema'
import { sameFenceSnapshot } from './state'

type Props = {
  first: WorkPage; host: HostDetail; target: string; signal: AbortSignal
  openVM: (id: string) => void; openMigration: (vm: string, id: string) => void
  openRecovery: (item: WorkItem) => void
  onCleanup?: (vm: string, id: string) => void
}

type PageProps = Props & { after: string; next: (after: string) => void; previous: () => void }

function itemStatus(item: WorkItem) {
  if (item.live_migration_id) return '实时迁移未结束'
  if (item.evacuation_id) return '疏散源端待清理'
  return vmStatus(item.state)
}

export function FenceWorkList(props: Props) {
  const [pages, setPages] = useState([''])
  const after = pages.at(-1) ?? ''
  const navigation = {
    after, next: (cursor: string) => setPages((current) => [...current, cursor]),
    previous: () => setPages((current) => current.slice(0, -1)),
  }
  return <section aria-label="重新接入阻塞项">
    <h3>重新接入阻塞项</h3>
    {after ? <FollowupPage key={after} {...props} {...navigation} /> : <WorkPageTable {...props} {...navigation} page={props.first} />}
  </section>
}

function FollowupPage(props: PageProps) {
  const { after, target, signal } = props
  const read = useCallback((path: string, requestSignal: AbortSignal) => readFenceWork(path, requestSignal, after), [after])
  const result = useResourceRead(target, read, signal)
  if (result.state === 'ready') return <WorkPageTable {...props} page={result.data} />
  return <>
    {result.state === 'loading' ? <p role="status">正在加载阻塞项…</p> :
      <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    <button type="button" onClick={props.previous}>上一页</button>
  </>
}

function WorkPageTable({ page, host, after, next, previous, openVM, openMigration, openRecovery, onCleanup }: PageProps & { page: WorkPage }) {
  if (!sameFenceSnapshot(host, page)) return <p role="alert">宿主机版本或代次已变化，请刷新隔离工作台。</p>
  const nextCursor = page.next
  return <>
    {page.dispatched_volume_cleanups > 0 && <p>已派发的保留卷清理任务 {page.dispatched_volume_cleanups} 项。</p>}
    {page.items.length === 0 ? <p>本页暂无虚拟机阻塞项。</p> : <ConsoleTable
      columns={['虚拟机', '状态', '任务版本', '观测与处置']}
      rows={page.items.map((item) => {
        const { vm_id: id, live_migration_id: migration, evacuation_id: evacuation } = item
        return { key: id + '/' + (migration ?? evacuation ?? item.placement_id), cells: [
          <button type="button" onClick={() => openVM(id)}>{id}</button>, itemStatus(item), item.vm_revision,
          <div>
            {item.observation && <span className="minor">{item.observation}</span>}
            {item.pending_recovery_id && <span className="minor">恢复待审 {item.pending_recovery_id}</span>}
            {(item.pending_recovery_id || observationsFor(item.state).length > 0) &&
              <button type="button" onClick={() => openRecovery(item)}>查看恢复与现场证据</button>}
            {migration && <button type="button" onClick={() => openMigration(id, migration)}>查看实时迁移回执</button>}
            {evacuation && <span className="minor">疏散 ID {evacuation}</span>}
            {evacuation && onCleanup && <button type="button" onClick={() => onCleanup(id, evacuation)}>登记源端清理证据</button>}
          </div>,
        ] }
      })} />}
    {(after || nextCursor) && <div className="row-actions">
      {after && <button type="button" onClick={previous}>上一页</button>}
      {nextCursor && <button type="button" onClick={() => next(nextCursor)}>下一页</button>}
    </div>}
  </>
}
