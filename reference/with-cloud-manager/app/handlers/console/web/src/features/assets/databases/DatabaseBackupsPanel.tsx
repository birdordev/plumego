import { useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { request } from '../../session/client'
import { databaseBackupPageSchema, type DatabaseBackupPage } from './schema'

type Props = { target: string; name: string; signal: AbortSignal }
type Position = { offset: number; stamp?: string }
const firstPage: Position = { offset: 0 }
const size = new Intl.NumberFormat('zh-CN')
const kinds = { full: '全量', incremental: '增量', fragment: '差异', unknown: '未提供' }
const modes = { automatic: '自动', manual: '手动', unknown: '未提供' }
const statuses = { ready: '已完成', pending: '处理中', failed: '失败', deleting: '删除中', unknown: '未知' }
const read = (target: string, signal: AbortSignal) => request(target, { signal }, databaseBackupPageSchema)

export default function DatabaseBackupsPanel({ target, name, signal }: Props) {
  const [position, setPosition] = useState(firstPage)
  const [reload, setReload] = useState(0)
  const url = target + '&offset=' + position.offset + (position.stamp ? '&observed_at=' + encodeURIComponent(position.stamp) : '')
  const restart = () => {
    setPosition(firstPage)
    setReload((value) => value + 1)
  }
  return <>
    <BackupPage key={url + '/' + reload} target={url} signal={signal} name={name} offset={position.offset} move={setPosition} restart={restart} />
    <button type="button" onClick={restart}>重新读取快照</button>
  </>
}

function BackupPage({ target, name, signal, offset, move, restart }: Props &
  { offset: number; move: (position: Position) => void; restart: () => void }) {
  const result = useResourceRead<DatabaseBackupPage>(target, read, signal)
  if (result.state === 'loading') return <p role="status">正在读取备份快照…</p>
  if (result.state === 'error') {
    const error = result.error
    if (error instanceof ConsoleRequestError && error.status === 404) return <p role="status">当前没有可用的备份快照。</p>
    if (error instanceof ConsoleRequestError && error.status === 409) return <p role="status">备份快照已更新。<button type="button" onClick={restart}>返回首页</button></p>
    return <p role="alert">{requestErrorMessage(error)}</p>
  }
  const page = result.data
  return <>
    <DetailFields fields={[
      ['数据库实例', name], ['观测时间', timestamp(page.observed_at)],
      ['查询范围', timestamp(page.window_start) + ' 至 ' + timestamp(page.window_end)],
      ['备份数量', page.total],
    ]} />
    {page.backups.length ? <ConsoleTable columns={['备份 ID', '类型', '方式', '状态', '开始时间', '结束时间', '大小']}
      rows={page.backups.map((backup) => ({
        key: backup.id,
        cells: [backup.id, kinds[backup.kind], modes[backup.mode], statuses[backup.status],
          timestamp(backup.started_at), backup.ended_at ? timestamp(backup.ended_at) : undefined,
          backup.size_bytes === undefined ? undefined : size.format(BigInt(backup.size_bytes)) + ' B'],
      }))} /> : <p>该查询窗口内没有备份记录。</p>}
    {offset > 0 ? <button type="button" onClick={() => move({ offset: Math.max(0, offset - 50), stamp: page.observed_at })}>上一页</button> : null}
    {page.next_offset !== undefined ? <button type="button" onClick={() => move({ offset: page.next_offset ?? 0, stamp: page.observed_at })}>下一页</button> : null}
  </>
}
