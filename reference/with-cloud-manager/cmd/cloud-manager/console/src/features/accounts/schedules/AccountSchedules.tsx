import { useState } from 'react'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { timestamp } from '../../../shared/display/time'
import type { Account } from '../schema'
import { changeSchedule, listSchedules, type Schedule } from './api'
import { scheduleKindLabel, scheduleRegionLabel } from './kinds'
import { ScheduleForm } from './ScheduleForm'

const outcomes = { success: '成功', failure: '失败', throttled: '限流' }

export function AccountSchedules({ account, target, stamp, signal, back }: {
  account: Account; target: string; stamp: number; signal: AbortSignal; back: () => void
}) {
  const [selected, setSelected] = useState<Schedule | 'new'>()
  const [refresh, setRefresh] = useState(0)
  const { busy, run } = useDetailAction(stamp)
  const readable = account.mode === 'read_only' || account.mode === 'write_enabled'
  function reload() {
    setDetailNotice(stamp)
    setSelected(undefined)
    setRefresh((value) => value + 1)
  }
  if (selected) return <ScheduleForm current={selected === 'new' ? undefined : selected}
    target={target} stamp={stamp} signal={signal} readable={readable} saved={reload} cancel={reload} />
  return <>
    <div className="row-actions">
      <button type="button" disabled={busy} onClick={back}>返回云账号</button>
      <button type="button" disabled={busy} onClick={reload}>刷新盘点计划</button>
      <button type="button" disabled={busy} onClick={() => setSelected('new')}>登记盘点计划</button>
    </div>
    <PagedResults key={refresh} target={target} signal={signal} read={listSchedules}>{(items) => items.length
      ? <ConsoleTable columns={['资源类型', '地域', '间隔（秒）', '状态', '最近结果', '下次执行', '操作']}
        rows={items.map((item) => ({ key: item.id, cells: [
          scheduleKindLabel(item.kind), scheduleRegionLabel(item.region), item.interval_seconds, item.enabled ? '已启用' : '已停用',
          <>{item.last_outcome === 'success' && item.bucket_batch_rejected ? '有桶未更新' : item.last_outcome && outcomes[item.last_outcome]}
            {item.bucket_batch_rejected != null && <span className="minor">最近提交批次未更新 {item.bucket_batch_rejected} 个桶</span>}</>,
          item.enabled ? timestamp(item.next_at) : undefined,
          <div className="row-actions">
            <button type="button" disabled={busy} onClick={() => setSelected(item)}>编辑</button>
            {readable && item.enabled && <button type="button" disabled={busy} onClick={() => void run(
              () => changeSchedule(target, item, { enabled: true, interval_seconds: item.interval_seconds, run_now: true }, signal), reload,
            )}>立即盘点</button>}
          </div>,
        ] }))} />
      : <p>暂无盘点计划。</p>}</PagedResults>
  </>
}
